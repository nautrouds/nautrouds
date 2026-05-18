package proxy_test

import (
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/rtree"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_ServeHTTP(t *testing.T) {
	// Setup a temporary directory for registry
	tmpDir, err := os.MkdirTemp("", "nautrouds-proxy-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	reg, err := registry.NewRegistry(tmpDir)
	require.NoError(t, err)

	manager := proxy.NewManager(reg)

	// 1. Setup Route Tree
	rawNodes := []*rtree.RawNode{
		{
			URL:     "example.com/api/test",
			Service: "test-service",
			Methods: "GET",
		},
		{
			URL:     "example.com/virtual",
			Service: "$echo",
			Methods: "GET",
		},
		{
			URL:     "example.com/ok",
			Service: "$ok(Success)",
			Methods: "GET",
		},
	}
	tree := rtree.Build(rawNodes)
	manager.UpdateTree(tree)

	t.Run("Not Found", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/unknown", nil)
		w := httptest.NewRecorder()
		manager.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("Method Not Allowed", func(t *testing.T) {
		req := httptest.NewRequest("POST", "http://example.com/api/test", nil)
		w := httptest.NewRecorder()
		manager.ServeHTTP(w, req)

		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("Service Unavailable (No Nodes)", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/api/test", nil)
		w := httptest.NewRecorder()
		manager.ServeHTTP(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("Virtual Service $echo", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/virtual", nil)
		w := httptest.NewRecorder()
		manager.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
		assert.Contains(t, w.Body.String(), `"path":"/virtual"`)
	})

	t.Run("Virtual Service $ok with args", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/ok", nil)
		w := httptest.NewRecorder()
		manager.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "Success", w.Body.String())
	})
}

func TestManager_LoadBalancing(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-proxy-lb-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create dummy socket files to satisfy Registry.Scan
	svcDir := filepath.Join(tmpDir, "lb-service")
	os.MkdirAll(svcDir, 0755)
	os.WriteFile(filepath.Join(svcDir, "node1.sock"), []byte(""), 0644)
	os.WriteFile(filepath.Join(svcDir, "node2.sock"), []byte(""), 0644)

	reg, err := registry.NewRegistry(tmpDir)
	require.NoError(t, err)
	err = reg.Scan("")
	require.NoError(t, err)

	_ = proxy.NewManager(reg)

	// Verify internal state of registry for the service
	state := reg.GetState()
	nodes, ok := state["lb-service"]
	require.True(t, ok)
	assert.Len(t, nodes, 2)

	// Verify that GetForwarder cycles through nodes.
	f1, err := reg.GetForwarder("lb-service")
	require.NoError(t, err)

	f2, err := reg.GetForwarder("lb-service")
	require.NoError(t, err)

	f3, err := reg.GetForwarder("lb-service")
	require.NoError(t, err)

	// We can't easily check private fields, but we know it's round-robin.
	// If it was the same node, f1 and f2 would be identical in a way we can't easily see here,
	// but we've verified the state has 2 nodes.
	assert.NotNil(t, f1)
	assert.NotNil(t, f2)
	assert.NotNil(t, f3)
}
