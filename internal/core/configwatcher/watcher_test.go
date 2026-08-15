package configwatcher_test

import (
	"nautrouds/internal/core/configwatcher"
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/rtree"
	"path/filepath"
	"testing"
)

func newTestManager(t *testing.T) *proxy.Manager {
	t.Helper()
	reg, err := registry.NewRegistry()
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}
	return proxy.NewManager(reg, nil)
}

func TestLoadInitial_MissingConfig_DefaultWelcomeEnabled(t *testing.T) {
	manager := newTestManager(t)
	configPath := filepath.Join(t.TempDir(), "missing.ntu")

	cw, err := configwatcher.NewConfigWatcher(configPath, "ntuc", manager, true)
	if err != nil {
		t.Fatalf("failed to create config watcher: %v", err)
	}
	defer cw.Close()

	if err := cw.LoadInitial(); err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}

	gen := manager.State.Load()
	if gen == nil {
		t.Fatal("expected a generation to be loaded")
	}

	url := []byte("any-host.example/any/path")
	rtree.ReverseHost(url)
	node, exists := gen.Tree.Search(url)
	if !exists {
		t.Fatal("expected fallback tree to match any request")
	}
	if node.Methods != rtree.MethodAny {
		t.Errorf("expected fallback route to accept any method, got %v", node.Methods)
	}
}

func TestLoadInitial_MissingConfig_DefaultWelcomeDisabled(t *testing.T) {
	manager := newTestManager(t)
	configPath := filepath.Join(t.TempDir(), "missing.ntu")

	cw, err := configwatcher.NewConfigWatcher(configPath, "ntuc", manager, false)
	if err != nil {
		t.Fatalf("failed to create config watcher: %v", err)
	}
	defer cw.Close()

	if err := cw.LoadInitial(); err == nil {
		t.Fatal("expected error when config is missing and fallback is disabled")
	}
}
