package proxy_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nautrouds/internal/core/metrics"
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/rtree"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestManager(t *testing.T, routes ...*rtree.RawNode) *proxy.Manager {
	t.Helper()
	reg, err := registry.NewRegistry()
	require.NoError(t, err)

	manager := proxy.NewManager(reg, nil)
	manager.UpdateGeneration(&proxy.Generation{Tree: *rtree.Build(routes)})
	return manager
}

func serve(manager *proxy.Manager, method, url string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, url, nil)
	manager.ServeHTTP(w, req)
	return w
}

func TestManager_ServeHTTP(t *testing.T) {
	tests := []struct {
		name            string
		route           *rtree.RawNode
		method          string
		url             string
		wantCode        int
		wantBody        string
		wantContentType string
	}{
		{
			name:     "NotFound",
			route:    &rtree.RawNode{URL: "example.com/api/test", Service: "test-service", Methods: "GET"},
			method:   "GET",
			url:      "http://example.com/unknown",
			wantCode: http.StatusNotFound,
		},
		{
			name:     "MethodNotAllowed",
			route:    &rtree.RawNode{URL: "example.com/api/test", Service: "test-service", Methods: "GET"},
			method:   "POST",
			url:      "http://example.com/api/test",
			wantCode: http.StatusMethodNotAllowed,
		},
		{
			name:     "ServiceUnavailableWithoutNodes",
			route:    &rtree.RawNode{URL: "example.com/api/test", Service: "test-service", Methods: "GET"},
			method:   "GET",
			url:      "http://example.com/api/test",
			wantCode: http.StatusServiceUnavailable,
		},
		{
			name:            "VirtualServiceEcho",
			route:           &rtree.RawNode{URL: "example.com/virtual", Service: "$echo", Methods: "GET"},
			method:          "GET",
			url:             "http://example.com/virtual",
			wantCode:        http.StatusOK,
			wantBody:        `"path":"/virtual"`,
			wantContentType: "application/json",
		},
		{
			name:     "VirtualServiceOKWithArgs",
			route:    &rtree.RawNode{URL: "example.com/ok", Service: "$ok(Success)", Methods: "GET"},
			method:   "GET",
			url:      "http://example.com/ok",
			wantCode: http.StatusOK,
			wantBody: "Success",
		},
		{
			name:            "VirtualServiceServices",
			route:           &rtree.RawNode{URL: "example.com/services", Service: "$services", Methods: "GET"},
			method:          "GET",
			url:             "http://example.com/services",
			wantCode:        http.StatusOK,
			wantBody:        "{",
			wantContentType: "application/json",
		},
		{
			name:     "VirtualServicePingNoNodes",
			route:    &rtree.RawNode{URL: "example.com/ping", Service: "$ping(missing-svc)", Methods: "GET"},
			method:   "GET",
			url:      "http://example.com/ping",
			wantCode: http.StatusServiceUnavailable,
		},
		{
			name:     "UnknownVirtualService",
			route:    &rtree.RawNode{URL: "example.com/bad", Service: "$nonexistent", Methods: "GET"},
			method:   "GET",
			url:      "http://example.com/bad",
			wantCode: http.StatusInternalServerError,
		},
		{
			name: "BuiltinMiddleware",
			route: &rtree.RawNode{
				URL:         "example.com/mw",
				Service:     "$echo",
				Methods:     "GET",
				Middlewares: []string{"$SetHeader(X-Injected, yes)"},
			},
			method:   "GET",
			url:      "http://example.com/mw",
			wantCode: http.StatusOK,
			wantBody: "X-Injected",
		},
		{
			name: "BuiltinMiddlewareBlocks",
			route: &rtree.RawNode{
				URL:         "example.com/secure",
				Service:     "$ok",
				Methods:     "GET",
				Middlewares: []string{"$BasicAuth(user, pass)"},
			},
			method:   "GET",
			url:      "http://example.com/secure",
			wantCode: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newTestManager(t, tt.route)
			w := serve(manager, tt.method, tt.url)

			assert.Equal(t, tt.wantCode, w.Code)
			assert.Contains(t, w.Body.String(), tt.wantBody)
			assert.Contains(t, w.Header().Get("Content-Type"), tt.wantContentType)
		})
	}
}

func TestServeHTTP_ResolveBuiltinMiddleware_Cache(t *testing.T) {
	manager := newTestManager(t,
		&rtree.RawNode{URL: "example.com/c1", Service: "$echo", Methods: "GET", Middlewares: []string{"$SetHeader(X-Hit, 1)"}},
		&rtree.RawNode{URL: "example.com/c2", Service: "$echo", Methods: "GET", Middlewares: []string{"$SetHeader(X-Hit, 1)"}},
	)

	for _, path := range []string{"/c1", "/c2"} {
		assert.Equal(t, http.StatusOK, serve(manager, "GET", "http://example.com"+path).Code)
	}
}

func TestManager_UpdateTree_HotSwap(t *testing.T) {
	manager := newTestManager(t, &rtree.RawNode{URL: "example.com/a", Service: "$ok(A)", Methods: "GET"})

	wA := serve(manager, "GET", "http://example.com/a")
	assert.Equal(t, http.StatusOK, wA.Code)
	assert.Equal(t, "A", wA.Body.String())

	manager.UpdateGeneration(&proxy.Generation{Tree: *rtree.Build([]*rtree.RawNode{
		{URL: "example.com/b", Service: "$ok(B)", Methods: "GET"},
	})})

	assert.Equal(t, http.StatusNotFound, serve(manager, "GET", "http://example.com/a").Code)
	wB := serve(manager, "GET", "http://example.com/b")
	assert.Equal(t, http.StatusOK, wB.Code)
	assert.Equal(t, "B", wB.Body.String())
}

func TestManager_LatencyMetrics(t *testing.T) {
	manager := newTestManager(t,
		&rtree.RawNode{URL: "metrics.example.com/tracked", Service: "$ok(tracked)", Methods: "GET"},
		&rtree.RawNode{URL: "metrics.example.com/silent", Service: "$ok(silent)", Methods: "GET", Tags: []string{"@no-metrics"}},
	)

	serve(manager, "GET", "http://metrics.example.com/tracked")
	serve(manager, "GET", "http://metrics.example.com/silent")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	metrics.Global.WritePrometheus(w, req)
	body := w.Body.String()

	assert.Contains(t, body, `route="$ok(tracked)"`, "tracked route should be labeled by its raw service declaration")
	assert.NotContains(t, body, "metrics.example.com/tracked", "duration metrics must not be labeled by the raw request path")
	assert.NotContains(t, body, `route="$ok(silent)"`, "a route tagged @no-metrics must not record duration metrics")
}
