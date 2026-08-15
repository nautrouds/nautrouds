package configwatcher_test

import (
	"bytes"
	"encoding/gob"
	"nautrouds/internal/compiler"
	"nautrouds/internal/core/configwatcher"
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/rtree"
	"os"
	"path/filepath"
	"testing"
)

// Not safe with t.Parallel(): os.Stdin is swapped globally for the duration.
func withPipedStdin(t *testing.T, data []byte) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	original := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = original
		r.Close()
	})

	if _, err := w.Write(data); err != nil {
		t.Fatalf("failed to write to pipe: %v", err)
	}
	w.Close()
}

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

func TestLoadInitial_MissingConfig_StdinPiped_UsesPipedConfig(t *testing.T) {
	tree, err := compiler.ParseString("GET only.example/from-stdin stdin-service")
	if err != nil {
		t.Fatalf("failed to build test tree: %v", err)
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(tree); err != nil {
		t.Fatalf("failed to encode test tree: %v", err)
	}
	withPipedStdin(t, buf.Bytes())

	manager := newTestManager(t)
	configPath := filepath.Join(t.TempDir(), "missing.ntu")

	cw, err := configwatcher.NewConfigWatcher(configPath, "ntuc", manager, true)
	if err != nil {
		t.Fatalf("failed to create config watcher: %v", err)
	}
	defer cw.Close()

	if err := cw.LoadInitial(); err != nil {
		t.Fatalf("expected stdin-piped config to load, got error: %v", err)
	}

	gen := manager.State.Load()
	url := []byte("only.example/from-stdin")
	rtree.ReverseHost(url)
	if _, exists := gen.Tree.Search(url); !exists {
		t.Fatal("expected route from piped stdin config to be present")
	}
}

func TestLoadInitial_MissingConfig_StdinPiped_DecodeFailureFailsFast(t *testing.T) {
	withPipedStdin(t, []byte("not a valid gob stream"))

	manager := newTestManager(t)
	configPath := filepath.Join(t.TempDir(), "missing.ntu")

	cw, err := configwatcher.NewConfigWatcher(configPath, "ntuc", manager, true)
	if err != nil {
		t.Fatalf("failed to create config watcher: %v", err)
	}
	defer cw.Close()

	if err := cw.LoadInitial(); err == nil {
		t.Fatal("expected error for undecodable piped stdin, not a silent welcome fallback")
	}

	if gen := manager.State.Load(); gen != nil {
		t.Fatal("expected no generation to be loaded after a failed stdin decode")
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
