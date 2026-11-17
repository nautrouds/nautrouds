package nodescan

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type serviceScanCall struct {
	serviceName string
	discovered  []string
}

type fakeHandler struct {
	ext string

	mu               sync.Mutex
	fullScanCalls    []map[string]map[string]struct{}
	serviceScanCalls []serviceScanCall
}

func (h *fakeHandler) Extension() string { return h.ext }

func (h *fakeHandler) ApplyFullScan(baseDir string, byService map[string]map[string]struct{}) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fullScanCalls = append(h.fullScanCalls, byService)
	return nil
}

func (h *fakeHandler) ApplyServiceScan(baseDir string, serviceName string, discovered []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.serviceScanCalls = append(h.serviceScanCalls, serviceScanCall{serviceName, discovered})
	return nil
}

func newTestScanner(t *testing.T, baseDir string, files ...string) (*Scanner, *fakeHandler, *fakeHandler) {
	t.Helper()
	for _, file := range files {
		path := filepath.Join(baseDir, file)
		dir := filepath.Dir(path)
		require.NoError(t, os.MkdirAll(dir, 0755))
		require.NoError(t, os.WriteFile(path, nil, 0644))
	}

	s, err := New(baseDir)
	require.NoError(t, err)

	sockHandler := &fakeHandler{ext: ".sock"}
	mmfgHandler := &fakeHandler{ext: ".mmfg"}
	require.NoError(t, s.Register(sockHandler))
	require.NoError(t, s.Register(mmfgHandler))
	return s, sockHandler, mmfgHandler
}

func countByService(byService map[string]map[string]struct{}) map[string]int {
	counts := make(map[string]int, len(byService))
	for service, nodes := range byService {
		counts[service] = len(nodes)
	}
	return counts
}

func TestScanAll(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		wantSock map[string]int
		wantMmfg map[string]int
	}{
		{
			name:     "DispatchesByExtensionAndService",
			files:    []string{"api/v1.sock", "api/v2.sock", "web/app.sock", "api/v1.mmfg"},
			wantSock: map[string]int{"api": 2, "web": 1},
			wantMmfg: map[string]int{"api": 1},
		},
		{
			name:     "IgnoresUnregisteredExtension",
			files:    []string{"api/v1.sock", "api/scratch.tmp"},
			wantSock: map[string]int{"api": 1},
			wantMmfg: map[string]int{},
		},
		{
			name:     "SkipsRootLevelFiles",
			files:    []string{"root.sock", "api/v1.sock"},
			wantSock: map[string]int{"api": 1},
			wantMmfg: map[string]int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, sockHandler, mmfgHandler := newTestScanner(t, t.TempDir(), tt.files...)
			require.NoError(t, s.scanAll())

			require.Len(t, sockHandler.fullScanCalls, 1)
			require.Len(t, mmfgHandler.fullScanCalls, 1)
			assert.Equal(t, tt.wantSock, countByService(sockHandler.fullScanCalls[0]))
			assert.Equal(t, tt.wantMmfg, countByService(mmfgHandler.fullScanCalls[0]))
		})
	}
}

func TestScanAll_MissingBaseDirNoError(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "does-not-exist")
	s, sockHandler, _ := newTestScanner(t, missingDir)

	require.NoError(t, s.scanAll())
	require.Len(t, sockHandler.fullScanCalls, 1)
	assert.Empty(t, sockHandler.fullScanCalls[0])
}

func TestScanService_CallsHandlerEvenWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	apiDir := filepath.Join(tmpDir, "api")
	require.NoError(t, os.MkdirAll(apiDir, 0755))
	s, sockHandler, _ := newTestScanner(t, tmpDir)

	require.NoError(t, s.scanService("api"))

	require.Len(t, sockHandler.serviceScanCalls, 1)
	assert.Equal(t, "api", sockHandler.serviceScanCalls[0].serviceName)
	assert.Empty(t, sockHandler.serviceScanCalls[0].discovered)
}

func TestScanService_DispatchesOnlyMatchingExtension(t *testing.T) {
	s, sockHandler, mmfgHandler := newTestScanner(t, t.TempDir(), "api/v1.sock", "api/v1.mmfg")

	require.NoError(t, s.scanService("api"))

	assert.Len(t, sockHandler.serviceScanCalls[0].discovered, 1)
	assert.Len(t, mmfgHandler.serviceScanCalls[0].discovered, 1)
}

func TestRegister_DuplicateExtensionErrors(t *testing.T) {
	s, err := New(t.TempDir())
	require.NoError(t, err)

	require.NoError(t, s.Register(&fakeHandler{ext: ".sock"}))
	assert.Error(t, s.Register(&fakeHandler{ext: ".sock"}))
}
