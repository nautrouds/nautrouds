package proxy_test

import (
	"fmt"
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/rtree"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newFailedNodeManager(t *testing.T, serviceName string, nodeCount int, middlewares []string) *proxy.Manager {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "nautrouds-failed-node-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	nodes := make([]string, nodeCount)
	listeners := make([]net.Listener, nodeCount)
	for i := range nodes {
		nodes[i] = filepath.Join(tmpDir, fmt.Sprintf("node%d.sock", i))
		l, err := net.Listen("unix", nodes[i])
		require.NoError(t, err)
		listeners[i] = l
	}

	reg, err := registry.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, serviceName, nodes))

	time.Sleep(50 * time.Millisecond)

	for _, l := range listeners {
		require.NoError(t, l.Close())
	}

	manager := proxy.NewManager(reg, nil)
	tree := rtree.Build([]*rtree.RawNode{
		{
			URL:         "example.com/api",
			Service:     serviceName,
			Methods:     "GET,POST",
			Middlewares: middlewares,
		},
	})
	manager.UpdateGeneration(&proxy.Generation{Tree: *tree})

	return manager
}

func TestForwardToBackend_SafeMethodRetriesThenServiceUnavailable(t *testing.T) {
	manager := newFailedNodeManager(t, "failed-svc-safe", 1, nil)

	req := httptest.NewRequest("GET", "http://example.com/api", nil)
	w := httptest.NewRecorder()
	manager.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestForwardToBackend_UnsafeMethodFailsFastWithoutRetry(t *testing.T) {
	manager := newFailedNodeManager(t, "failed-svc-unsafe", 1, nil)

	req := httptest.NewRequest("POST", "http://example.com/api", nil)
	w := httptest.NewRecorder()
	manager.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestForwardToBackend_RetryLimitBoundsAttempts(t *testing.T) {
	manager := newFailedNodeManager(t, "failed-svc-limited", 3, []string{"$RetryLimit(1)"})

	req := httptest.NewRequest("GET", "http://example.com/api", nil)
	w := httptest.NewRecorder()
	manager.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestForwardToBackend_NoRetryLimitExhaustsAllNodes(t *testing.T) {
	manager := newFailedNodeManager(t, "failed-svc-unbounded", 3, nil)

	req := httptest.NewRequest("GET", "http://example.com/api", nil)
	w := httptest.NewRecorder()
	manager.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestForwardToBackend_ErrNodeFailedSkipIgnoresRetryLimit(t *testing.T) {
	serviceName := "failed-svc-node-failed-skip"

	tmpDir, err := os.MkdirTemp("", "nautrouds-node-failed-skip-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	nodePath := filepath.Join(tmpDir, "node@3.sock")
	l, err := net.Listen("unix", nodePath)
	require.NoError(t, err)

	reg, err := registry.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, serviceName, []string{nodePath}))

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, l.Close())

	manager := proxy.NewManager(reg, nil)
	tree := rtree.Build([]*rtree.RawNode{
		{
			URL:         "example.com/api",
			Service:     serviceName,
			Methods:     "GET",
			Middlewares: []string{"$RetryLimit(1)"},
		},
	})
	manager.UpdateGeneration(&proxy.Generation{Tree: *tree})

	req := httptest.NewRequest("GET", "http://example.com/api", nil)
	w := httptest.NewRecorder()
	manager.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}
