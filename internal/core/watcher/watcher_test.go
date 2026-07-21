package watcher

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nautrouds/internal/core/registry"
)

func TestWatcher_Basic(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-watcher-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	reg, err := registry.NewRegistry()
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	w, err := NewWatcher(tmpDir, reg)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer w.Close()

	// Initial start should perform a scan
	err = w.Start()
	if err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}

	// For simplicity in unit test, we verify that Start() didn't crash and initialized the registry.
	if w.scanner.BaseDir() != filepath.ToSlash(tmpDir) {
		t.Errorf("expected base dir %s, got %s", filepath.ToSlash(tmpDir), w.scanner.BaseDir())
	}
}

func TestWatcher_ScanOnStart(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-watcher-start-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Pre-create some structure
	svcDir := filepath.Join(tmpDir, "test-svc")
	os.MkdirAll(svcDir, 0755)
	os.WriteFile(filepath.Join(svcDir, "1.sock"), []byte(""), 0644)

	reg, err := registry.NewRegistry()
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	w, err := NewWatcher(tmpDir, reg)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer w.Close()

	if err := w.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

}

func TestWatcher_CloseWithoutStart(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-watcher-close-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	reg, err := registry.NewRegistry()
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	w, err := NewWatcher(tmpDir, reg)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Close without calling Start — cancel is nil, should not panic
	if err := w.Close(); err != nil {
		t.Errorf("unexpected error on Close without Start: %v", err)
	}
}

func TestWatcher_StartEmptyDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-watcher-empty-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	reg, err := registry.NewRegistry()
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	w, err := NewWatcher(tmpDir, reg)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer w.Close()

	if err := w.Start(); err != nil {
		t.Fatalf("Start on empty dir failed: %v", err)
	}

	state := reg.GetState()
	if len(state) != 0 {
		t.Errorf("expected empty state after scanning empty dir, got %v", state)
	}
}

func TestWatcher_EventDrivenScan(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-watcher-event-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	svcDir := filepath.Join(tmpDir, "api")
	os.MkdirAll(svcDir, 0755)

	reg, err := registry.NewRegistry()
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	w, err := NewWatcher(tmpDir, reg)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer w.Close()

	if err := w.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Create a real socket so the probe succeeds and node stays healthy
	sockPath := filepath.Join(svcDir, "node.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to create socket: %v", err)
	}
	defer ln.Close()

	// Wait for the watcher to pick up the fs event and run the scan.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		state := reg.GetState()
		if _, ok := state["api"]; ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Error("timed out waiting for event-driven scan to register api service")
}
