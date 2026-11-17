//go:build unix

package mmfg

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeKeyAndIsControlSocket(t *testing.T) {
	tests := []struct {
		path        string
		wantKey     string
		wantControl bool
	}{
		{"/base/api/v1.mmfg", "api/v1", false},
		{"/base/api/v1.ctl.mmfg", "api/v1", true},
		{"/base/web/app.mmfg", "web/app", false},
		{"/base/v1.mmfg", "v1", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.wantControl, isControlSocket(tt.path))
			key, err := nodeKey("/base", tt.path)
			require.NoError(t, err)
			assert.Equal(t, tt.wantKey, key)
		})
	}
}

type fakeDial struct {
	mu    sync.Mutex
	calls []dialCall
	fail  map[string]bool
}

type dialCall struct {
	nodeName, socketPath, controlSocketPath string
}

func (f *fakeDial) dial(nodeName, socketPath, controlSocketPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, dialCall{nodeName, socketPath, controlSocketPath})
	if f.fail[socketPath] {
		return fmt.Errorf("dial failed for %s", socketPath)
	}
	return nil
}

func (f *fakeDial) callsFor(socketPath string) []dialCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var matched []dialCall
	for _, c := range f.calls {
		if c.socketPath == socketPath {
			matched = append(matched, c)
		}
	}
	return matched
}

func newTestHub() (*UnixHub, *fakeDial) {
	fd := &fakeDial{fail: make(map[string]bool)}
	return &UnixHub{dial: fd.dial, dialed: make(map[string]string)}, fd
}

func (h *UnixHub) isDialed(socketPath string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.dialed[socketPath]
	return ok
}

func TestUnixHub_ApplyServiceScan_ControlSocketPairing(t *testing.T) {
	tests := []struct {
		name       string
		discovered []string
		wantCalls  []dialCall
	}{
		{
			name:       "PairsControlSocket",
			discovered: []string{"/base/api/v1.mmfg", "/base/api/v1.ctl.mmfg"},
			wantCalls:  []dialCall{{"api/v1", "/base/api/v1.mmfg", "/base/api/v1.ctl.mmfg"}},
		},
		{
			name:       "MainWithoutControl",
			discovered: []string{"/base/api/v1.mmfg"},
			wantCalls:  []dialCall{{"api/v1", "/base/api/v1.mmfg", ""}},
		},
		{
			name:       "OrphanControlSocketNotDialed",
			discovered: []string{"/base/api/v1.ctl.mmfg"},
			wantCalls:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, fd := newTestHub()
			require.NoError(t, h.ApplyServiceScan("/base", "api", tt.discovered))
			assert.Equal(t, tt.wantCalls, fd.calls)
		})
	}
}

func TestUnixHub_ApplyServiceScan_DialsOnceThenSkips(t *testing.T) {
	h, fd := newTestHub()

	require.NoError(t, h.ApplyServiceScan("/base", "api", []string{"/base/api/v1.mmfg"}))
	require.Len(t, fd.calls, 1)

	require.NoError(t, h.ApplyServiceScan("/base", "api", []string{"/base/api/v1.mmfg", "/base/api/v1.ctl.mmfg"}))
	assert.Len(t, fd.calls, 1)
}

func TestUnixHub_ApplyServiceScan_PartialFailureContinuesAndRetries(t *testing.T) {
	h, fd := newTestHub()
	fd.fail["/base/api/bad.mmfg"] = true
	discovered := []string{"/base/api/good.mmfg", "/base/api/bad.mmfg"}

	require.Error(t, h.ApplyServiceScan("/base", "api", discovered))
	assert.Len(t, fd.callsFor("/base/api/good.mmfg"), 1)
	assert.True(t, h.isDialed("/base/api/good.mmfg"))
	assert.False(t, h.isDialed("/base/api/bad.mmfg"))

	require.Error(t, h.ApplyServiceScan("/base", "api", discovered))
	assert.Len(t, fd.callsFor("/base/api/bad.mmfg"), 2)
}

func TestUnixHub_ApplyServiceScan_PrunesRemovedNode(t *testing.T) {
	h, _ := newTestHub()

	require.NoError(t, h.ApplyServiceScan("/base", "api", []string{"/base/api/v1.mmfg", "/base/api/v2.mmfg"}))
	require.NoError(t, h.ApplyServiceScan("/base", "api", []string{"/base/api/v1.mmfg"}))

	assert.True(t, h.isDialed("/base/api/v1.mmfg"))
	assert.False(t, h.isDialed("/base/api/v2.mmfg"))
}

func TestUnixHub_ApplyFullScan_DialsAcrossServicesWithControlPairing(t *testing.T) {
	h, fd := newTestHub()

	byService := map[string]map[string]struct{}{
		"api": {
			"/base/api/v1.mmfg":     {},
			"/base/api/v1.ctl.mmfg": {},
		},
		"web": {
			"/base/web/app.mmfg": {},
		},
	}
	require.NoError(t, h.ApplyFullScan("/base", byService))
	assert.ElementsMatch(t, []dialCall{
		{"api/v1", "/base/api/v1.mmfg", "/base/api/v1.ctl.mmfg"},
		{"web/app", "/base/web/app.mmfg", ""},
	}, fd.calls)

	byService = map[string]map[string]struct{}{
		"web": {
			"/base/web/app.mmfg": {},
		},
	}
	require.NoError(t, h.ApplyFullScan("/base", byService))
	assert.False(t, h.isDialed("/base/api/v1.mmfg"))
}
