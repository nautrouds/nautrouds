package watcher

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nautrouds/internal/core/registry"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatcher_StartEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	reg, err := registry.NewRegistry()
	require.NoError(t, err)

	w, err := NewWatcher(tmpDir, reg)
	require.NoError(t, err)
	defer w.Close()

	require.NoError(t, w.Start())
	assert.Equal(t, filepath.ToSlash(tmpDir), w.scanner.BaseDir())
	assert.Empty(t, reg.GetState())
}

func TestWatcher_ScanOnStart(t *testing.T) {
	tmpDir := t.TempDir()
	svcDir := filepath.Join(tmpDir, "test-svc")
	require.NoError(t, os.MkdirAll(svcDir, 0755))
	sockPath := filepath.Join(svcDir, "1.sock")
	require.NoError(t, os.WriteFile(sockPath, []byte(""), 0644))

	reg, err := registry.NewRegistry()
	require.NoError(t, err)

	w, err := NewWatcher(tmpDir, reg)
	require.NoError(t, err)
	defer w.Close()

	assert.NoError(t, w.Start())
}

func TestWatcher_CloseWithoutStart(t *testing.T) {
	reg, err := registry.NewRegistry()
	require.NoError(t, err)

	w, err := NewWatcher(t.TempDir(), reg)
	require.NoError(t, err)

	assert.NoError(t, w.Close())
}

func TestWatcher_EventDrivenScan(t *testing.T) {
	tmpDir := t.TempDir()
	svcDir := filepath.Join(tmpDir, "api")
	require.NoError(t, os.MkdirAll(svcDir, 0755))

	reg, err := registry.NewRegistry()
	require.NoError(t, err)

	w, err := NewWatcher(tmpDir, reg)
	require.NoError(t, err)
	defer w.Close()
	require.NoError(t, w.Start())

	sockPath := filepath.Join(svcDir, "node.sock")
	ln, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	defer ln.Close()

	assert.Eventually(t, func() bool {
		_, ok := reg.GetState()["api"]
		return ok
	}, 8*time.Second, 20*time.Millisecond)
}
