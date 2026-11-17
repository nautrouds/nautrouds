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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Not safe with t.Parallel(): os.Stdin is swapped globally for the duration.
func withPipedStdin(t *testing.T, data []byte) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = original
		r.Close()
	})

	_, err = w.Write(data)
	require.NoError(t, err)
	w.Close()
}

func newTestWatcher(t *testing.T, defaultWelcome bool) (*configwatcher.ConfigWatcher, *proxy.Manager) {
	t.Helper()
	reg, err := registry.NewRegistry()
	require.NoError(t, err)
	manager := proxy.NewManager(reg, nil)

	configPath := filepath.Join(t.TempDir(), "missing.ntu")
	cw, err := configwatcher.NewConfigWatcher(configPath, "ntuc", manager, defaultWelcome)
	require.NoError(t, err)
	t.Cleanup(func() { cw.Close() })
	return cw, manager
}

func TestLoadInitial_MissingConfig_DefaultWelcomeEnabled(t *testing.T) {
	cw, manager := newTestWatcher(t, true)
	require.NoError(t, cw.LoadInitial())

	gen := manager.State.Load()
	require.NotNil(t, gen)

	url := []byte("any-host.example/any/path")
	rtree.ReverseHost(url)
	node, exists := gen.Tree.Search(url)
	require.True(t, exists)
	assert.Equal(t, rtree.MethodAny, node.Methods)
}

func TestLoadInitial_MissingConfig_StdinPiped_UsesPipedConfig(t *testing.T) {
	tree, err := compiler.ParseString("GET only.example/from-stdin stdin-service")
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(tree))
	pipedConfig := buf.Bytes()
	withPipedStdin(t, pipedConfig)

	cw, manager := newTestWatcher(t, true)
	require.NoError(t, cw.LoadInitial())

	url := []byte("only.example/from-stdin")
	rtree.ReverseHost(url)
	_, exists := manager.State.Load().Tree.Search(url)
	assert.True(t, exists)
}

func TestLoadInitial_MissingConfig_StdinPiped_DecodeFailureFailsFast(t *testing.T) {
	withPipedStdin(t, []byte("not a valid gob stream"))

	cw, manager := newTestWatcher(t, true)
	require.Error(t, cw.LoadInitial())
	assert.Nil(t, manager.State.Load())
}

func TestLoadInitial_MissingConfig_DefaultWelcomeDisabled(t *testing.T) {
	cw, _ := newTestWatcher(t, false)
	assert.Error(t, cw.LoadInitial())
}
