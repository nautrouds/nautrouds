package registry

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"nautrouds/internal/core/registry/forwarder"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serveUnixOK(t *testing.T, socketPath string) {
	t.Helper()

	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	go server.Serve(l)

	t.Cleanup(func() {
		server.Shutdown(context.Background())
		l.Close()
	})
}

func TestApplyServiceScan_WeightedRoundRobinDistribution(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-weight-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	serviceDir := filepath.Join(tmpDir, "svc")
	require.NoError(t, os.MkdirAll(serviceDir, 0755))

	lightPath := filepath.Join(serviceDir, "node-0@1.sock")
	heavyPath := filepath.Join(serviceDir, "node-1@3.sock")
	serveUnixOK(t, lightPath)
	serveUnixOK(t, heavyPath)

	reg, err := NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{lightPath, heavyPath}))

	reg.mu.RLock()
	light := reg.nodeMap[lightPath].forwarder
	heavy := reg.nodeMap[heavyPath].forwarder
	reg.mu.RUnlock()

	require.EqualValues(t, 1, light.Weight())
	require.EqualValues(t, 3, heavy.Weight())

	counts := map[*forwarder.Forwarder]int{}
	const rounds = 100
	for range rounds * 4 {
		result := reg.GetForwarders("svc")
		require.Len(t, result, 4)
		counts[result[0]]++
	}

	assert.Equal(t, rounds, counts[light])
	assert.Equal(t, rounds*3, counts[heavy])
}

func TestApplyFullScan_WeightedRoundRobinDistribution(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-weight-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	serviceDir := filepath.Join(tmpDir, "svc")
	require.NoError(t, os.MkdirAll(serviceDir, 0755))

	lightPath := filepath.Join(serviceDir, "node-0@1.sock")
	heavyPath := filepath.Join(serviceDir, "node-1@3.sock")
	serveUnixOK(t, lightPath)
	serveUnixOK(t, heavyPath)

	reg, err := NewRegistry()
	require.NoError(t, err)

	byService := map[string]map[string]struct{}{
		"svc": {lightPath: {}, heavyPath: {}},
	}
	require.NoError(t, reg.ApplyFullScan(tmpDir, byService))

	reg.mu.RLock()
	light := reg.nodeMap[lightPath].forwarder
	heavy := reg.nodeMap[heavyPath].forwarder
	reg.mu.RUnlock()

	require.EqualValues(t, 1, light.Weight())
	require.EqualValues(t, 3, heavy.Weight())

	counts := map[*forwarder.Forwarder]int{}
	const rounds = 100
	for range rounds * 4 {
		result := reg.GetForwarders("svc")
		require.Len(t, result, 4)
		counts[result[0]]++
	}

	assert.Equal(t, rounds, counts[light])
	assert.Equal(t, rounds*3, counts[heavy])
}
