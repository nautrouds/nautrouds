package watcher

import (
	"os"
	"path/filepath"
	"testing"

	"nautrouds/internal/core/registry"
)

func TestWatcher_Basic(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-watcher-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	reg, err := registry.NewRegistry(tmpDir)
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	w, err := NewWatcher(reg)
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
	if reg.BaseDir() != filepath.ToSlash(tmpDir) {
		t.Errorf("expected base dir %s, got %s", filepath.ToSlash(tmpDir), reg.BaseDir())
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

	reg, err := registry.NewRegistry(tmpDir)
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	w, err := NewWatcher(reg)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer w.Close()

	if err := w.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

}
