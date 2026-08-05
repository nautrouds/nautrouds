package proxy_test

import (
	"context"
	"io"
	"nautrouds/internal/core/mmfg"
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/rtree"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const chunkedContentLength = -1

// fakeMmfgRequest is a minimal mmfg.Request stub that records how many times
// Apply is called, so tests can assert it fires exactly once.
type fakeMmfgRequest struct {
	applyCount atomic.Int32
}

func (f *fakeMmfgRequest) Inject(req *http.Request) error                    { return nil }
func (f *fakeMmfgRequest) Cookies() ([]*http.Cookie, error)                  { return nil, nil }
func (f *fakeMmfgRequest) SetCookie(name string, value string) error         { return nil }
func (f *fakeMmfgRequest) DeleteCookie(name string) error                    { return nil }
func (f *fakeMmfgRequest) Method() (string, error)                           { return "", nil }
func (f *fakeMmfgRequest) SetMethod(method string) error                     { return nil }
func (f *fakeMmfgRequest) URL() (*url.URL, error)                            { return nil, nil }
func (f *fakeMmfgRequest) SetURL(rawURL string) error                        { return nil }
func (f *fakeMmfgRequest) Header(key string) (string, error)                 { return "", nil }
func (f *fakeMmfgRequest) CloneHeaders() (http.Header, error)                { return make(http.Header), nil }
func (f *fakeMmfgRequest) UpdateHeader(key string, newValue ...string) error { return nil }
func (f *fakeMmfgRequest) DeleteHeader(key string) error                     { return nil }
func (f *fakeMmfgRequest) Next(nodeName string) (bool, error)                { return false, nil }
func (f *fakeMmfgRequest) Apply() error                                      { f.applyCount.Add(1); return nil }
func (f *fakeMmfgRequest) AcceptSelfResponse(w http.ResponseWriter) error    { return nil }

var _ mmfg.Request = (*fakeMmfgRequest)(nil)

type fakeMmfgHub struct {
	req *fakeMmfgRequest
}

func (h *fakeMmfgHub) Extension() string { return ".mmfg" }
func (h *fakeMmfgHub) ApplyFullScan(baseDir string, byService map[string]map[string]struct{}) error {
	return nil
}
func (h *fakeMmfgHub) ApplyServiceScan(baseDir string, serviceName string, discovered []string) error {
	return nil
}
func (h *fakeMmfgHub) Request(ctx context.Context, r *http.Request) (mmfg.Request, error) {
	return h.req, nil
}

var _ mmfg.Hub = (*fakeMmfgHub)(nil)

// TestManager_BodySizeLimit_AppliesAndDropsMmfgSession verifies the
// runMiddlewareChain special case: when $BodySizeLimit runs with an open
// mmfg session, the session's Apply() must fire exactly once (committing
// pending mmfg mutations before the body check) and must not be reused or
// re-applied afterwards.
//
// mmfg is unix-only (internal/core/mmfg/mmfg_not_unix.go), so this test only
// runs where mmfg.IsAvailable is true.
func TestManager_BodySizeLimit_AppliesAndDropsMmfgSession(t *testing.T) {
	if !mmfg.IsAvailable {
		t.Skip("mmfg is unix-only; skipping on this platform")
	}

	fakeReq := &fakeMmfgRequest{}
	hub := &fakeMmfgHub{req: fakeReq}

	reg, err := registry.NewRegistry()
	require.NoError(t, err)

	manager := proxy.NewManager(reg, hub)

	rawNodes := []*rtree.RawNode{
		{
			URL:         "example.com/upload",
			Service:     "upload-service",
			Methods:     "POST",
			Middlewares: []string{"$mmfg(test-node)", "$BodySizeLimit(10)"},
		},
	}
	tree := rtree.Build(rawNodes)
	manager.UpdateGeneration(&proxy.Generation{Tree: *tree})

	req := httptest.NewRequest("POST", "http://example.com/upload", strings.NewReader("this body is way over ten bytes"))
	w := httptest.NewRecorder()
	manager.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Equal(t, int32(1), fakeReq.applyCount.Load())
}

func TestServeHTTP_ChunkedBodyExceedsBodySizeLimit_ReturnsRequestEntityTooLarge(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "proxy-body-too-large-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	svcDir := filepath.Join(tmpDir, "upload-service")
	require.NoError(t, os.MkdirAll(svcDir, 0755))
	socketPath := filepath.Join(svcDir, "node.sock")

	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer l.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		}),
	}
	go server.Serve(l)
	defer server.Shutdown(context.Background())

	reg, err := registry.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "upload-service", []string{socketPath}))

	manager := proxy.NewManager(reg, nil)

	tree := rtree.Build([]*rtree.RawNode{
		{
			URL:         "example.com/upload",
			Service:     "upload-service",
			Methods:     "POST",
			Middlewares: []string{"$BodySizeLimit(10)"},
		},
	})
	manager.UpdateGeneration(&proxy.Generation{Tree: *tree})

	req := httptest.NewRequest("POST", "http://example.com/upload", strings.NewReader("this body is way over the ten byte limit"))
	req.ContentLength = chunkedContentLength
	w := httptest.NewRecorder()
	manager.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}
