//go:build unix

package test

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"nautrouds/internal/core/mmfg"
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/rtree"

	"github.com/nautrouds/mmfg-http/go/mmfghttp"
	"github.com/nautrouds/mmfg/v2/go/node"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMmfgTestEnv sets up a Registry + real unix mmfg.Hub wired into a
// proxy.Manager, backed by a fresh temp services directory.
func newMmfgTestEnv(t *testing.T) (manager *proxy.Manager, reg *registry.Registry, hub mmfg.Hub, servicesDir string) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "nautrouds-mmfg-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	servicesDir = filepath.Join(tmpDir, "services")
	require.NoError(t, os.MkdirAll(servicesDir, 0755))

	reg, err = registry.NewRegistry()
	require.NoError(t, err)

	hub, err = mmfg.NewHub()
	require.NoError(t, err)

	manager = proxy.NewManager(reg, hub)
	return manager, reg, hub, servicesDir
}

// startBackendEcho listens on a real unix socket under
// <servicesDir>/<serviceName>/instance.sock and echoes path/query/headers as
// JSON, so tests can assert on what actually reached the backend.
func startBackendEcho(t *testing.T, servicesDir, serviceName string) string {
	t.Helper()

	svcDir := filepath.Join(servicesDir, serviceName)
	require.NoError(t, os.MkdirAll(svcDir, 0755))
	socketPath := filepath.Join(svcDir, "instance.sock")

	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"path":    r.URL.Path,
				"query":   r.URL.RawQuery,
				"headers": r.Header,
			})
		}),
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	return socketPath
}

// startMmfgNode binds socketPath synchronously (avoiding any race with a
// caller that dials right after this returns) and serves it with handler.
func startMmfgNode(t *testing.T, socketPath string, handler node.Handler) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(socketPath), 0755))
	os.Remove(socketPath)

	l, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: socketPath, Net: "unixpacket"})
	require.NoError(t, err)

	n := node.NewNode(node.WithHandler(handler))
	go n.Serve(l)
	t.Cleanup(func() { l.Close() })
}

// startMmfgControl binds a node's control socket synchronously and serves it
// with handler, used for self-respond scenarios.
func startMmfgControl(t *testing.T, socketPath string, handler mmfghttp.Handler) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(socketPath), 0755))
	os.Remove(socketPath)

	l, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: socketPath, Net: "unixpacket"})
	require.NoError(t, err)

	go mmfghttp.Serve(l, handler)
	t.Cleanup(func() { l.Close() })
}

func TestIntegration_Mmfg_HeaderAndURLMutation(t *testing.T) {
	manager, reg, hub, servicesDir := newMmfgTestEnv(t)

	backendSocket := startBackendEcho(t, servicesDir, "backend-service")
	require.NoError(t, reg.ApplyServiceScan(servicesDir, "backend-service", []string{backendSocket}))

	nodeSocket := filepath.Join(servicesDir, "mmfg-service", "echo.mmfg")
	startMmfgNode(t, nodeSocket, func(conn node.Connection) {
		r := mmfghttp.New(conn)

		u, err := r.URL()
		if err != nil {
			return
		}

		if err := r.UpdateHeader("X-Mmfg-Processed", "true"); err != nil {
			return
		}
		if err := r.DeleteHeader("X-To-Remove"); err != nil {
			return
		}

		q := u.Query()
		q.Set("mmfg", "1")
		u.RawQuery = q.Encode()
		r.SetURL(u.String())
	})

	require.NoError(t, hub.ApplyServiceScan(servicesDir, "mmfg-service", []string{nodeSocket}))

	rawNode := &rtree.RawNode{
		URL:         "example.com/**",
		Service:     "backend-service",
		Middlewares: []string{"$mmfg(mmfg-service/echo)"},
		Methods:     "GET",
	}
	manager.UpdateGeneration(&proxy.Generation{Tree: *rtree.Build([]*rtree.RawNode{rawNode})})

	req := httptest.NewRequest("GET", "http://example.com/foo?a=1", nil)
	req.Header.Set("X-To-Remove", "bye")
	w := httptest.NewRecorder()

	manager.ServeHTTP(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, "/foo", body["path"])
	assert.Contains(t, body["query"], "mmfg=1")

	headers, ok := body["headers"].(map[string]interface{})
	require.True(t, ok, "expected headers field in backend echo response")

	require.Contains(t, headers, "X-Mmfg-Processed")
	assert.Equal(t, "true", headers["X-Mmfg-Processed"].([]interface{})[0])
	assert.NotContains(t, headers, "X-To-Remove")
}

func TestIntegration_Mmfg_NodeUnreachable(t *testing.T) {
	manager, _, _, _ := newMmfgTestEnv(t)

	rawNode := &rtree.RawNode{
		URL:         "example.com/**",
		Service:     "backend-service",
		Middlewares: []string{"$mmfg(mmfg-service/does-not-exist)"},
		Methods:     "GET",
	}
	manager.UpdateGeneration(&proxy.Generation{Tree: *rtree.Build([]*rtree.RawNode{rawNode})})

	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	w := httptest.NewRecorder()

	manager.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
}

func TestIntegration_Mmfg_SelfRespond(t *testing.T) {
	manager, _, hub, servicesDir := newMmfgTestEnv(t)

	const selfResponseBody = "self-response-from-node"

	mainSocket := filepath.Join(servicesDir, "mmfg-service", "selfresponder.mmfg")
	controlSocket := filepath.Join(servicesDir, "mmfg-service", "selfresponder.ctl.mmfg")

	startMmfgNode(t, mainSocket, func(conn node.Connection) {
		r := mmfghttp.New(conn)

		u, err := r.URL()
		if err != nil {
			return
		}
		if u.Path == "/self" {
			r.SelfRespond()
		}
	})

	startMmfgControl(t, controlSocket, mmfghttp.HandlerFunc(func(w mmfghttp.ResponseWriter, r *http.Request) {
		w.Write([]byte(selfResponseBody))
	}))

	require.NoError(t, hub.ApplyServiceScan(servicesDir, "mmfg-service", []string{mainSocket, controlSocket}))

	rawNode := &rtree.RawNode{
		URL:         "*/self",
		Service:     "unused-service",
		Middlewares: []string{"$mmfg(mmfg-service/selfresponder)"},
		Methods:     "GET",
	}
	manager.UpdateGeneration(&proxy.Generation{Tree: *rtree.Build([]*rtree.RawNode{rawNode})})

	// Self-respond hijacks the ResponseWriter, so it needs a real net/http
	// connection — httptest.NewRecorder() does not implement http.Hijacker.
	srv := httptest.NewServer(manager)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/self")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, selfResponseBody, string(body))
}
