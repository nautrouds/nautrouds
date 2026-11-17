package proxy_test

import (
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/rtree"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFailedNodeManager(t *testing.T, nodeNames []string, middlewares []string) *proxy.Manager {
	t.Helper()
	tmpDir := t.TempDir()

	nodes := make([]string, len(nodeNames))
	listeners := make([]net.Listener, len(nodeNames))
	for i, name := range nodeNames {
		nodes[i] = filepath.Join(tmpDir, name)
		l, err := net.Listen("unix", nodes[i])
		require.NoError(t, err)
		listeners[i] = l
	}

	reg, err := registry.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "failed-svc", nodes))

	time.Sleep(50 * time.Millisecond)

	for _, l := range listeners {
		require.NoError(t, l.Close())
	}

	manager := proxy.NewManager(reg, nil)
	tree := rtree.Build([]*rtree.RawNode{
		{
			URL:         "example.com/api",
			Service:     "failed-svc",
			Methods:     "GET,POST",
			Middlewares: middlewares,
		},
	})
	manager.UpdateGeneration(&proxy.Generation{Tree: *tree})

	return manager
}

func TestForwardToBackend_FailedNodes(t *testing.T) {
	threeNodes := []string{"node0.sock", "node1.sock", "node2.sock"}

	tests := []struct {
		name        string
		method      string
		nodes       []string
		middlewares []string
		wantCode    int
	}{
		{"SafeMethodRetriesThenServiceUnavailable", "GET", []string{"node0.sock"}, nil, http.StatusServiceUnavailable},
		{"UnsafeMethodFailsFastWithoutRetry", "POST", []string{"node0.sock"}, nil, http.StatusBadGateway},
		{"RetryLimitBoundsAttempts", "GET", threeNodes, []string{"$RetryLimit(1)"}, http.StatusBadGateway},
		{"NoRetryLimitExhaustsAllNodes", "GET", threeNodes, nil, http.StatusServiceUnavailable},
		{"ErrNodeFailedSkipIgnoresRetryLimit", "GET", []string{"node@3.sock"}, []string{"$RetryLimit(1)"}, http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newFailedNodeManager(t, tt.nodes, tt.middlewares)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, "http://example.com/api", nil)
			manager.ServeHTTP(w, req)

			assert.Equal(t, tt.wantCode, w.Code)
		})
	}
}
