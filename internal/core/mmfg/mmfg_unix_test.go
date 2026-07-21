//go:build unix

package mmfg

import (
	"fmt"
	"sync"
	"testing"
)

func TestNodeKeyAndIsControlSocket(t *testing.T) {
	cases := []struct {
		baseDir     string
		path        string
		wantKey     string
		wantControl bool
	}{
		{"/base", "/base/api/v1.mmfg", "api/v1", false},
		{"/base", "/base/api/v1.ctl.mmfg", "api/v1", true},
		{"/base", "/base/web/app.mmfg", "web/app", false},
		{"/base", "/base/v1.mmfg", "v1", false},
	}

	for _, c := range cases {
		if got := isControlSocket(c.path); got != c.wantControl {
			t.Errorf("isControlSocket(%q) = %v, want %v", c.path, got, c.wantControl)
		}
		key, err := nodeKey(c.baseDir, c.path)
		if err != nil {
			t.Fatalf("nodeKey(%q, %q) error: %v", c.baseDir, c.path, err)
		}
		if key != c.wantKey {
			t.Errorf("nodeKey(%q, %q) = %q, want %q", c.baseDir, c.path, key, c.wantKey)
		}
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

func newFakeDial() *fakeDial {
	return &fakeDial{fail: make(map[string]bool)}
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

func (f *fakeDial) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeDial) callFor(socketPath string) (dialCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.socketPath == socketPath {
			return c, true
		}
	}
	return dialCall{}, false
}

func newTestHub(fd *fakeDial) *UnixHub {
	return &UnixHub{
		dial:   fd.dial,
		dialed: make(map[string]string),
	}
}

func TestUnixHub_ApplyServiceScan_PairsControlSocket(t *testing.T) {
	fd := newFakeDial()
	h := newTestHub(fd)

	err := h.ApplyServiceScan("/base", "api", []string{"/base/api/v1.mmfg", "/base/api/v1.ctl.mmfg"})
	if err != nil {
		t.Fatalf("ApplyServiceScan error: %v", err)
	}

	call, ok := fd.callFor("/base/api/v1.mmfg")
	if !ok {
		t.Fatalf("expected dial call for main socket, got calls: %+v", fd.calls)
	}
	if call.nodeName != "api/v1" {
		t.Errorf("nodeName = %q, want %q", call.nodeName, "api/v1")
	}
	if call.controlSocketPath != "/base/api/v1.ctl.mmfg" {
		t.Errorf("controlSocketPath = %q, want %q", call.controlSocketPath, "/base/api/v1.ctl.mmfg")
	}
	if fd.callCount() != 1 {
		t.Errorf("expected exactly 1 dial call, got %d: %+v", fd.callCount(), fd.calls)
	}
}

func TestUnixHub_ApplyServiceScan_MainWithoutControl_DialsWithEmptyControl(t *testing.T) {
	fd := newFakeDial()
	h := newTestHub(fd)

	if err := h.ApplyServiceScan("/base", "api", []string{"/base/api/v1.mmfg"}); err != nil {
		t.Fatalf("ApplyServiceScan error: %v", err)
	}

	call, ok := fd.callFor("/base/api/v1.mmfg")
	if !ok {
		t.Fatalf("expected dial call for main socket")
	}
	if call.controlSocketPath != "" {
		t.Errorf("controlSocketPath = %q, want empty", call.controlSocketPath)
	}
}

func TestUnixHub_ApplyServiceScan_OrphanControlSocket_NotDialedAlone(t *testing.T) {
	fd := newFakeDial()
	h := newTestHub(fd)

	if err := h.ApplyServiceScan("/base", "api", []string{"/base/api/v1.ctl.mmfg"}); err != nil {
		t.Fatalf("ApplyServiceScan error: %v", err)
	}

	if fd.callCount() != 0 {
		t.Errorf("expected no dial calls for orphan control socket, got %+v", fd.calls)
	}
}

func TestUnixHub_ApplyServiceScan_DialsOnceThenSkips(t *testing.T) {
	fd := newFakeDial()
	h := newTestHub(fd)

	discovered := []string{"/base/api/v1.mmfg"}
	if err := h.ApplyServiceScan("/base", "api", discovered); err != nil {
		t.Fatalf("first ApplyServiceScan error: %v", err)
	}
	if fd.callCount() != 1 {
		t.Fatalf("expected 1 dial call after first scan, got %d", fd.callCount())
	}

	discovered = []string{"/base/api/v1.mmfg", "/base/api/v1.ctl.mmfg"}
	if err := h.ApplyServiceScan("/base", "api", discovered); err != nil {
		t.Fatalf("second ApplyServiceScan error: %v", err)
	}
	if fd.callCount() != 1 {
		t.Errorf("expected dial to still be called only once, got %d: %+v", fd.callCount(), fd.calls)
	}
}

func TestUnixHub_ApplyServiceScan_PartialFailureContinuesAndRetries(t *testing.T) {
	fd := newFakeDial()
	fd.fail["/base/api/bad.mmfg"] = true
	h := newTestHub(fd)

	discovered := []string{"/base/api/good.mmfg", "/base/api/bad.mmfg"}
	err := h.ApplyServiceScan("/base", "api", discovered)
	if err == nil {
		t.Fatal("expected error from failed dial")
	}
	if _, ok := fd.callFor("/base/api/good.mmfg"); !ok {
		t.Error("expected good node to still be dialed despite bad node failing")
	}

	h.mu.Lock()
	_, goodDialed := h.dialed["/base/api/good.mmfg"]
	_, badDialed := h.dialed["/base/api/bad.mmfg"]
	h.mu.Unlock()
	if !goodDialed {
		t.Error("expected good node to be recorded as dialed")
	}
	if badDialed {
		t.Error("expected failed node to not be recorded as dialed")
	}

	if err := h.ApplyServiceScan("/base", "api", discovered); err == nil {
		t.Fatal("expected error to persist on retry")
	}
	badCalls := 0
	fd.mu.Lock()
	for _, c := range fd.calls {
		if c.socketPath == "/base/api/bad.mmfg" {
			badCalls++
		}
	}
	fd.mu.Unlock()
	if badCalls != 2 {
		t.Errorf("expected bad node to be retried, got %d calls", badCalls)
	}
}

func TestUnixHub_ApplyServiceScan_PrunesRemovedNode(t *testing.T) {
	fd := newFakeDial()
	h := newTestHub(fd)

	discovered := []string{"/base/api/v1.mmfg", "/base/api/v2.mmfg"}
	if err := h.ApplyServiceScan("/base", "api", discovered); err != nil {
		t.Fatalf("ApplyServiceScan error: %v", err)
	}

	if err := h.ApplyServiceScan("/base", "api", []string{"/base/api/v1.mmfg"}); err != nil {
		t.Fatalf("ApplyServiceScan error: %v", err)
	}

	h.mu.Lock()
	_, v1Dialed := h.dialed["/base/api/v1.mmfg"]
	_, v2Dialed := h.dialed["/base/api/v2.mmfg"]
	h.mu.Unlock()
	if !v1Dialed {
		t.Error("expected v1 to remain tracked as dialed")
	}
	if v2Dialed {
		t.Error("expected v2 to be pruned from dialed set after disappearing")
	}
}

func TestUnixHub_ApplyFullScan_DialsAcrossServicesWithControlPairing(t *testing.T) {
	fd := newFakeDial()
	h := newTestHub(fd)

	byService := map[string]map[string]struct{}{
		"api": {
			"/base/api/v1.mmfg":     {},
			"/base/api/v1.ctl.mmfg": {},
		},
		"web": {
			"/base/web/app.mmfg": {},
		},
	}

	if err := h.ApplyFullScan("/base", byService); err != nil {
		t.Fatalf("ApplyFullScan error: %v", err)
	}

	apiCall, ok := fd.callFor("/base/api/v1.mmfg")
	if !ok || apiCall.controlSocketPath != "/base/api/v1.ctl.mmfg" {
		t.Errorf("expected api node to be dialed with control path, got %+v (ok=%v)", apiCall, ok)
	}
	webCall, ok := fd.callFor("/base/web/app.mmfg")
	if !ok || webCall.controlSocketPath != "" {
		t.Errorf("expected web node to be dialed without control path, got %+v (ok=%v)", webCall, ok)
	}
	if fd.callCount() != 2 {
		t.Errorf("expected 2 dial calls, got %d: %+v", fd.callCount(), fd.calls)
	}

	byService = map[string]map[string]struct{}{
		"web": {
			"/base/web/app.mmfg": {},
		},
	}
	if err := h.ApplyFullScan("/base", byService); err != nil {
		t.Fatalf("second ApplyFullScan error: %v", err)
	}
	h.mu.Lock()
	_, apiStillDialed := h.dialed["/base/api/v1.mmfg"]
	h.mu.Unlock()
	if apiStillDialed {
		t.Error("expected api node to be pruned after disappearing from full scan")
	}
}

func TestUnixHub_Extension(t *testing.T) {
	h := &UnixHub{}
	if got := h.Extension(); got != ".mmfg" {
		t.Errorf("Extension() = %q, want %q", got, ".mmfg")
	}
}
