package nodescan

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
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

func newFakeHandler(ext string) *fakeHandler {
	return &fakeHandler{ext: ext}
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

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatalf("failed to create file %s: %v", path, err)
	}
}

func TestScanAll_DispatchesByExtensionAndService(t *testing.T) {
	tmpDir := t.TempDir()

	touch(t, filepath.Join(tmpDir, "api", "v1.sock"))
	touch(t, filepath.Join(tmpDir, "api", "v2.sock"))
	touch(t, filepath.Join(tmpDir, "web", "app.sock"))
	touch(t, filepath.Join(tmpDir, "api", "v1.mmfg"))

	s, err := New(tmpDir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sockHandler := newFakeHandler(".sock")
	mmfgHandler := newFakeHandler(".mmfg")
	if err := s.Register(sockHandler); err != nil {
		t.Fatalf("Register(.sock) failed: %v", err)
	}
	if err := s.Register(mmfgHandler); err != nil {
		t.Fatalf("Register(.mmfg) failed: %v", err)
	}

	if err := s.scanAll(); err != nil {
		t.Fatalf("ScanAll failed: %v", err)
	}

	if len(sockHandler.fullScanCalls) != 1 {
		t.Fatalf("expected 1 ApplyFullScan call on sock handler, got %d", len(sockHandler.fullScanCalls))
	}
	sockResult := sockHandler.fullScanCalls[0]
	if len(sockResult["api"]) != 2 {
		t.Errorf("expected 2 .sock nodes for api, got %v", sockResult["api"])
	}
	if len(sockResult["web"]) != 1 {
		t.Errorf("expected 1 .sock node for web, got %v", sockResult["web"])
	}
	if _, ok := sockResult["api"]; !ok || len(sockResult) != 2 {
		t.Errorf("unexpected sock result shape: %v", sockResult)
	}

	if len(mmfgHandler.fullScanCalls) != 1 {
		t.Fatalf("expected 1 ApplyFullScan call on mmfg handler, got %d", len(mmfgHandler.fullScanCalls))
	}
	mmfgResult := mmfgHandler.fullScanCalls[0]
	if len(mmfgResult) != 1 || len(mmfgResult["api"]) != 1 {
		t.Errorf("expected exactly 1 .mmfg node under api, got %v", mmfgResult)
	}
}

func TestScanAll_IgnoresUnregisteredExtension(t *testing.T) {
	tmpDir := t.TempDir()

	touch(t, filepath.Join(tmpDir, "api", "v1.sock"))
	touch(t, filepath.Join(tmpDir, "api", "scratch.tmp"))

	s, err := New(tmpDir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sockHandler := newFakeHandler(".sock")
	if err := s.Register(sockHandler); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if err := s.scanAll(); err != nil {
		t.Fatalf("ScanAll failed: %v", err)
	}

	result := sockHandler.fullScanCalls[0]
	if len(result["api"]) != 1 {
		t.Errorf("expected only the .sock file to be discovered, got %v", result)
	}
}

func TestScanAll_SkipsRootLevelFiles(t *testing.T) {
	tmpDir := t.TempDir()

	touch(t, filepath.Join(tmpDir, "root.sock"))
	touch(t, filepath.Join(tmpDir, "api", "v1.sock"))

	s, err := New(tmpDir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sockHandler := newFakeHandler(".sock")
	if err := s.Register(sockHandler); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if err := s.scanAll(); err != nil {
		t.Fatalf("ScanAll failed: %v", err)
	}

	result := sockHandler.fullScanCalls[0]
	if len(result) != 1 {
		t.Errorf("expected only the 'api' service to be present, got %v", result)
	}
	if len(result["api"]) != 1 {
		t.Errorf("expected 1 node under api, got %v", result["api"])
	}
}

func TestScanService_CallsHandlerEvenWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(tmpDir, "api"), 0755); err != nil {
		t.Fatalf("failed to create service dir: %v", err)
	}

	s, err := New(tmpDir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sockHandler := newFakeHandler(".sock")
	if err := s.Register(sockHandler); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if err := s.scanService("api"); err != nil {
		t.Fatalf("ScanService failed: %v", err)
	}

	if len(sockHandler.serviceScanCalls) != 1 {
		t.Fatalf("expected ApplyServiceScan to be called exactly once even with no matches, got %d calls", len(sockHandler.serviceScanCalls))
	}
	call := sockHandler.serviceScanCalls[0]
	if call.serviceName != "api" {
		t.Errorf("expected serviceName 'api', got %q", call.serviceName)
	}
	if len(call.discovered) != 0 {
		t.Errorf("expected empty discovered slice, got %v", call.discovered)
	}
}

func TestScanService_DispatchesOnlyMatchingExtension(t *testing.T) {
	tmpDir := t.TempDir()

	touch(t, filepath.Join(tmpDir, "api", "v1.sock"))
	touch(t, filepath.Join(tmpDir, "api", "v1.mmfg"))

	s, err := New(tmpDir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sockHandler := newFakeHandler(".sock")
	mmfgHandler := newFakeHandler(".mmfg")
	if err := s.Register(sockHandler); err != nil {
		t.Fatalf("Register(.sock) failed: %v", err)
	}
	if err := s.Register(mmfgHandler); err != nil {
		t.Fatalf("Register(.mmfg) failed: %v", err)
	}

	if err := s.scanService("api"); err != nil {
		t.Fatalf("ScanService failed: %v", err)
	}

	if len(sockHandler.serviceScanCalls[0].discovered) != 1 {
		t.Errorf("expected 1 .sock node, got %v", sockHandler.serviceScanCalls[0].discovered)
	}
	if len(mmfgHandler.serviceScanCalls[0].discovered) != 1 {
		t.Errorf("expected 1 .mmfg node, got %v", mmfgHandler.serviceScanCalls[0].discovered)
	}
}

func TestRegister_DuplicateExtensionErrors(t *testing.T) {
	tmpDir := t.TempDir()

	s, err := New(tmpDir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err := s.Register(newFakeHandler(".sock")); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}

	if err := s.Register(newFakeHandler(".sock")); err == nil {
		t.Error("expected error registering duplicate extension, got nil")
	}
}

func TestScanAll_MissingBaseDirNoError(t *testing.T) {
	tmpDir := t.TempDir()
	missing := filepath.Join(tmpDir, "does-not-exist")

	s, err := New(missing)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sockHandler := newFakeHandler(".sock")
	if err := s.Register(sockHandler); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if err := s.scanAll(); err != nil {
		t.Errorf("expected no error for missing base dir, got %v", err)
	}
	if len(sockHandler.fullScanCalls) != 1 {
		t.Fatalf("expected handler to still be called once, got %d", len(sockHandler.fullScanCalls))
	}
	if len(sockHandler.fullScanCalls[0]) != 0 {
		t.Errorf("expected empty result, got %v", sockHandler.fullScanCalls[0])
	}
}
