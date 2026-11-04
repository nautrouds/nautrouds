package proxy_test

import (
	"nautrouds/internal/core/metrics"
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/rtree"
	"net"
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

	reg, err := registry.NewRegistry()
	require.NoError(t, err)

	manager := proxy.NewManager(reg, nil)

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
	manager.UpdateGeneration(&proxy.Generation{Tree: *tree})

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

func TestManager_LatencyMetrics(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-proxy-metrics-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	reg, err := registry.NewRegistry()
	require.NoError(t, err)

	manager := proxy.NewManager(reg, nil)

	rawNodes := []*rtree.RawNode{
		{
			URL:     "metrics.example.com/tracked",
			Service: "$ok(tracked)",
			Methods: "GET",
		},
		{
			URL:     "metrics.example.com/silent",
			Service: "$ok(silent)",
			Methods: "GET",
			Tags:    []string{"@no-metrics"},
		},
	}
	tree := rtree.Build(rawNodes)
	manager.UpdateGeneration(&proxy.Generation{Tree: *tree})

	req := httptest.NewRequest("GET", "http://metrics.example.com/tracked", nil)
	manager.ServeHTTP(httptest.NewRecorder(), req)

	req = httptest.NewRequest("GET", "http://metrics.example.com/silent", nil)
	manager.ServeHTTP(httptest.NewRecorder(), req)

	w := httptest.NewRecorder()
	metrics.Global.WritePrometheus(w, httptest.NewRequest("GET", "/metrics", nil))
	body := w.Body.String()

	assert.Contains(t, body, `route="$ok(tracked)"`, "tracked route should be labeled by its raw service declaration")
	assert.NotContains(t, body, "metrics.example.com/tracked", "duration metrics must not be labeled by the raw request path")
	assert.NotContains(t, body, `route="$ok(silent)"`, "a route tagged @no-metrics must not record duration metrics")
}

func TestManager_LoadBalancing(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-proxy-lb-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	svcDir := filepath.Join(tmpDir, "lb-service")
	require.NoError(t, os.MkdirAll(svcDir, 0755))

	nodes := []string{
		filepath.Join(svcDir, "node1.sock"),
		filepath.Join(svcDir, "node2.sock"),
	}
	for _, node := range nodes {
		l, err := net.Listen("unix", node)
		require.NoError(t, err)
		t.Cleanup(func() { l.Close() })
	}

	reg, err := registry.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "lb-service", nodes))

	_ = proxy.NewManager(reg, nil)

	state := reg.GetState()
	registered, ok := state["lb-service"]
	require.True(t, ok)
	assert.Len(t, registered, 2)

	for range 3 {
		forwarders := reg.GetForwarders("lb-service")
		require.NotEmpty(t, forwarders)
		assert.NotNil(t, forwarders[0])
	}
}
