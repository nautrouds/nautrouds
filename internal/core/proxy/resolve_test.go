package proxy_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/rtree"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestManager(t *testing.T) (*proxy.Manager, *registry.Registry, string) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "proxy-resolve-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	reg, err := registry.NewRegistry()
	require.NoError(t, err)

	return proxy.NewManager(reg, nil), reg, tmpDir
}

func TestServeHTTP_VirtualService_Services(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	tree := rtree.Build([]*rtree.RawNode{
		{URL: "example.com/services", Service: "$services", Methods: "GET"},
	})
	mgr.UpdateGeneration(&proxy.Generation{Tree: *tree})

	req := httptest.NewRequest("GET", "http://example.com/services", nil)
	w := httptest.NewRecorder()
	mgr.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var body map[string][]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
}

func TestServeHTTP_VirtualService_Ping_NoNodes(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	tree := rtree.Build([]*rtree.RawNode{
		{URL: "example.com/ping", Service: "$ping(missing-svc)", Methods: "GET"},
	})
	mgr.UpdateGeneration(&proxy.Generation{Tree: *tree})

	req := httptest.NewRequest("GET", "http://example.com/ping", nil)
	w := httptest.NewRecorder()
	mgr.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestServeHTTP_UnknownVirtualService(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	tree := rtree.Build([]*rtree.RawNode{
		{URL: "example.com/bad", Service: "$nonexistent", Methods: "GET"},
	})
	mgr.UpdateGeneration(&proxy.Generation{Tree: *tree})

	req := httptest.NewRequest("GET", "http://example.com/bad", nil)
	w := httptest.NewRecorder()
	mgr.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestServeHTTP_BuiltinMiddleware(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	tree := rtree.Build([]*rtree.RawNode{
		{
			URL:         "example.com/mw",
			Service:     "$echo",
			Methods:     "GET",
			Middlewares: []string{"$SetHeader(X-Injected, yes)"},
		},
	})
	mgr.UpdateGeneration(&proxy.Generation{Tree: *tree})

	req := httptest.NewRequest("GET", "http://example.com/mw", nil)
	w := httptest.NewRecorder()
	mgr.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// $echo returns a JSON body containing the request headers
	assert.Contains(t, w.Body.String(), "X-Injected")
}

func TestServeHTTP_BuiltinMiddleware_Blocks(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	// BasicAuth with no credentials in the request → 401, stops chain
	tree := rtree.Build([]*rtree.RawNode{
		{
			URL:         "example.com/secure",
			Service:     "$ok",
			Methods:     "GET",
			Middlewares: []string{"$BasicAuth(user, pass)"},
		},
	})
	mgr.UpdateGeneration(&proxy.Generation{Tree: *tree})

	req := httptest.NewRequest("GET", "http://example.com/secure", nil)
	w := httptest.NewRecorder()
	mgr.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestServeHTTP_ResolveBuiltinMiddleware_Cache(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	tree := rtree.Build([]*rtree.RawNode{
		{URL: "example.com/c1", Service: "$echo", Methods: "GET", Middlewares: []string{"$SetHeader(X-Hit, 1)"}},
		{URL: "example.com/c2", Service: "$echo", Methods: "GET", Middlewares: []string{"$SetHeader(X-Hit, 1)"}},
	})
	mgr.UpdateGeneration(&proxy.Generation{Tree: *tree})

	// Two requests exercising the same middleware expression — second call goes through cache.
	for _, path := range []string{"/c1", "/c2"} {
		req := httptest.NewRequest("GET", "http://example.com"+path, nil)
		w := httptest.NewRecorder()
		mgr.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}
}

func TestManager_UpdateTree_HotSwap(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	// Install tree with route A
	mgr.UpdateGeneration(&proxy.Generation{Tree: *rtree.Build([]*rtree.RawNode{
		{URL: "example.com/a", Service: "$ok(A)", Methods: "GET"},
	})})

	reqA := httptest.NewRequest("GET", "http://example.com/a", nil)
	wA := httptest.NewRecorder()
	mgr.ServeHTTP(wA, reqA)
	assert.Equal(t, http.StatusOK, wA.Code)
	assert.Equal(t, "A", wA.Body.String())

	// Swap to tree with route B only — A should now 404
	mgr.UpdateGeneration(&proxy.Generation{Tree: *rtree.Build([]*rtree.RawNode{
		{URL: "example.com/b", Service: "$ok(B)", Methods: "GET"},
	})})

	reqA2 := httptest.NewRequest("GET", "http://example.com/a", nil)
	wA2 := httptest.NewRecorder()
	mgr.ServeHTTP(wA2, reqA2)
	assert.Equal(t, http.StatusNotFound, wA2.Code)

	reqB := httptest.NewRequest("GET", "http://example.com/b", nil)
	wB := httptest.NewRecorder()
	mgr.ServeHTTP(wB, reqB)
	assert.Equal(t, http.StatusOK, wB.Code)
	assert.Equal(t, "B", wB.Body.String())
}
