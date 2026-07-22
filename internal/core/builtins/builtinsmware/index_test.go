package builtinsmware_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"nautrouds/internal/core/builtins/builtinsmware"
	"nautrouds/internal/core/mmfg"
	"nautrouds/internal/core/tempresp"

	"github.com/stretchr/testify/assert"
)

// fakeMmfgRequest is a minimal mmfg.Request stub for exercising middleware
// code paths that branch on a non-nil mmfg.Request.
type fakeMmfgRequest struct {
	headers map[string]string
}

func (f *fakeMmfgRequest) Inject(req *http.Request) error                    { return nil }
func (f *fakeMmfgRequest) Cookies() ([]*http.Cookie, error)                  { return nil, nil }
func (f *fakeMmfgRequest) SetCookie(name string, value string) error         { return nil }
func (f *fakeMmfgRequest) DeleteCookie(name string) error                    { return nil }
func (f *fakeMmfgRequest) Method() (string, error)                           { return "", nil }
func (f *fakeMmfgRequest) SetMethod(method string) error                     { return nil }
func (f *fakeMmfgRequest) URL() (*url.URL, error)                            { return nil, nil }
func (f *fakeMmfgRequest) SetURL(rawURL string) error                        { return nil }
func (f *fakeMmfgRequest) Header(key string) (string, error)                 { return f.headers[key], nil }
func (f *fakeMmfgRequest) CloneHeaders() (http.Header, error)                { return make(http.Header), nil }
func (f *fakeMmfgRequest) UpdateHeader(key string, newValue ...string) error { return nil }
func (f *fakeMmfgRequest) DeleteHeader(key string) error                     { return nil }
func (f *fakeMmfgRequest) Next(nodeName string) (bool, error)                { return false, nil }
func (f *fakeMmfgRequest) Apply() error                                      { return nil }
func (f *fakeMmfgRequest) AcceptSelfResponse(w http.ResponseWriter) error    { return nil }

var _ mmfg.Request = (*fakeMmfgRequest)(nil)

func newWriter() (*tempresp.ResponseWriter, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	w := tempresp.Pool.Get().(*tempresp.ResponseWriter)
	w.Setup(rec)
	return w, rec
}

func TestSetHeader(t *testing.T) {
	fn := builtinsmware.SetHeader("X-Custom", "hello")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req, nil)
	assert.Equal(t, "hello", req.Header.Get("X-Custom"))
}

func TestSetHeader_Overwrite(t *testing.T) {
	fn := builtinsmware.SetHeader("X-Custom", "new")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Custom", "old")
	fn(w, req, nil)
	assert.Equal(t, "new", req.Header.Get("X-Custom"))
}

func TestDelHeader(t *testing.T) {
	fn := builtinsmware.DelHeader("X-Remove")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Remove", "value")
	fn(w, req, nil)
	assert.Empty(t, req.Header.Get("X-Remove"))
}

func TestDelHeader_NonExistent(t *testing.T) {
	fn := builtinsmware.DelHeader("X-Missing")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	// Should not panic when deleting a header that doesn't exist
	assert.NotPanics(t, func() { fn(w, req, nil) })
}

func TestSetHost(t *testing.T) {
	fn := builtinsmware.SetHost("override.example.com")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req, nil)
	assert.Equal(t, "override.example.com", req.Host)
}

func TestPathTrimPrefix_Matches(t *testing.T) {
	fn := builtinsmware.PathTrimPrefix("/api")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/api/users", nil)
	fn(w, req, nil)
	assert.Equal(t, "/users", req.URL.Path)
}

func TestPathTrimPrefix_NoMatch(t *testing.T) {
	fn := builtinsmware.PathTrimPrefix("/admin")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/api/users", nil)
	fn(w, req, nil)
	assert.Equal(t, "/api/users", req.URL.Path)
}

func TestPathTrimPrefix_UpdatesRequestURI(t *testing.T) {
	fn := builtinsmware.PathTrimPrefix("/v1")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/v1/items?q=test", nil)
	fn(w, req, nil)
	assert.Equal(t, "/items", req.URL.Path)
	assert.Equal(t, "/items?q=test", req.RequestURI)
}

func TestRewritePath(t *testing.T) {
	fn := builtinsmware.RewritePath("/old", "/new")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/old/resource", nil)
	fn(w, req, nil)
	assert.Equal(t, "/new/resource", req.URL.Path)
}

func TestRewritePath_NoMatch(t *testing.T) {
	fn := builtinsmware.RewritePath("/old", "/new")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/other/resource", nil)
	fn(w, req, nil)
	assert.Equal(t, "/other/resource", req.URL.Path)
}

func TestSetQuery_AddsKey(t *testing.T) {
	fn := builtinsmware.SetQuery("version", "2")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/api", nil)
	fn(w, req, nil)
	assert.Equal(t, "2", req.URL.Query().Get("version"))
}

func TestSetQuery_PreservesExisting(t *testing.T) {
	fn := builtinsmware.SetQuery("version", "2")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/api?existing=true", nil)
	fn(w, req, nil)
	assert.Equal(t, "2", req.URL.Query().Get("version"))
	assert.Equal(t, "true", req.URL.Query().Get("existing"))
}

func TestBasicAuth_ValidCredentials(t *testing.T) {
	fn := builtinsmware.BasicAuth("admin", "secret")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "secret")
	fn(w, req, nil)
	assert.Equal(t, http.StatusOK, w.GetCode())
}

func TestBasicAuth_WrongPassword(t *testing.T) {
	fn := builtinsmware.BasicAuth("admin", "secret")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "wrong")
	fn(w, req, nil)
	assert.Equal(t, http.StatusUnauthorized, w.GetCode())
}

func TestBasicAuth_NoHeader(t *testing.T) {
	fn := builtinsmware.BasicAuth("admin", "secret")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req, nil)
	assert.Equal(t, http.StatusUnauthorized, w.GetCode())
}

func TestBasicAuth_SetsWWWAuthenticate(t *testing.T) {
	fn := builtinsmware.BasicAuth("admin", "secret")
	w, rec := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req, nil)
	// Commit to recorder so we can inspect headers
	w.Commit()
	assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Basic")
}

func TestBasicAuth_Mmfg_ValidCredentials(t *testing.T) {
	fn := builtinsmware.BasicAuth("admin", "secret")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	mr := &fakeMmfgRequest{headers: map[string]string{"Authorization": "Basic YWRtaW46c2VjcmV0"}} // admin:secret
	fn(w, req, mr)
	assert.Equal(t, http.StatusOK, w.GetCode())
}

func TestBasicAuth_Mmfg_WrongPassword(t *testing.T) {
	fn := builtinsmware.BasicAuth("admin", "secret")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	mr := &fakeMmfgRequest{headers: map[string]string{"Authorization": "Basic YWRtaW46d3Jvbmc="}} // admin:wrong
	fn(w, req, mr)
	assert.Equal(t, http.StatusUnauthorized, w.GetCode())
}

func TestBasicAuth_Mmfg_NoHeader(t *testing.T) {
	fn := builtinsmware.BasicAuth("admin", "secret")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	mr := &fakeMmfgRequest{headers: map[string]string{}}
	fn(w, req, mr)
	assert.Equal(t, http.StatusUnauthorized, w.GetCode())
}

func TestRequireHeader_Match(t *testing.T) {
	fn := builtinsmware.RequireHeader("X-Internal", "yes")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Internal", "yes")
	fn(w, req, nil)
	assert.Equal(t, http.StatusOK, w.GetCode())
}

func TestRequireHeader_Mismatch(t *testing.T) {
	fn := builtinsmware.RequireHeader("X-Internal", "yes")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Internal", "no")
	fn(w, req, nil)
	assert.Equal(t, http.StatusForbidden, w.GetCode())
}

func TestRequireHeader_Missing(t *testing.T) {
	fn := builtinsmware.RequireHeader("X-Internal", "yes")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req, nil)
	assert.Equal(t, http.StatusForbidden, w.GetCode())
}

func TestRequireHeader_Mmfg_Match(t *testing.T) {
	fn := builtinsmware.RequireHeader("X-Internal", "yes")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	mr := &fakeMmfgRequest{headers: map[string]string{"X-Internal": "yes"}}
	fn(w, req, mr)
	assert.Equal(t, http.StatusOK, w.GetCode())
}

func TestRequireHeader_Mmfg_Mismatch(t *testing.T) {
	fn := builtinsmware.RequireHeader("X-Internal", "yes")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	mr := &fakeMmfgRequest{headers: map[string]string{}}
	fn(w, req, mr)
	assert.Equal(t, http.StatusForbidden, w.GetCode())
}

func TestIPAllow_AllowedIP(t *testing.T) {
	fn := builtinsmware.IPAllow("192.0.2.0/24")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	fn(w, req, nil)
	assert.Equal(t, http.StatusOK, w.GetCode())
}

func TestIPAllow_BlockedIP(t *testing.T) {
	fn := builtinsmware.IPAllow("10.0.0.0/8")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	fn(w, req, nil)
	assert.Equal(t, http.StatusForbidden, w.GetCode())
}

func TestIPAllow_InvalidCIDR_FallsBackToInvalidMiddleware(t *testing.T) {
	fn := builtinsmware.IPAllow("not-a-cidr")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req, nil)
	assert.Equal(t, http.StatusInternalServerError, w.GetCode())
}

func TestIPAllow_WrongArgCount_FallsBackToInvalidMiddleware(t *testing.T) {
	fn := builtinsmware.IPAllow("10.0.0.0/8", "extra")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req, nil)
	assert.Equal(t, http.StatusInternalServerError, w.GetCode())
}

func TestLog_DoesNotPanic(t *testing.T) {
	fn := builtinsmware.Log("TEST")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/path", nil)
	req.RemoteAddr = "127.0.0.1:9999"
	assert.NotPanics(t, func() { fn(w, req, nil) })
}

func TestInvalidMiddleware(t *testing.T) {
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	builtinsmware.InvalidMiddleware(w, req, nil)
	assert.Equal(t, http.StatusInternalServerError, w.GetCode())
}

func TestIsValid(t *testing.T) {
	tests := []struct {
		expr      string
		wantValid bool
	}{
		{"$SetHeader(X-Foo, bar)", true},
		{"$DelHeader(X-Old)", true},
		{"$PathTrimPrefix", true},
		{"$RewritePath(/old, /new)", true},
		{"$SetQuery(k, v)", true},
		{"$BasicAuth(user, pass)", true},
		{"$RequireHeader(X-Internal, yes)", true},
		{"$IPAllow(10.0.0.0/8)", true},
		{"$Log(prefix)", true},
		{"$UnknownMiddleware", false},
		{"notabuiltin", false},
		{"$SetHeader(unclosed", false},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			valid, _ := builtinsmware.IsValid(tt.expr)
			assert.Equal(t, tt.wantValid, valid)
		})
	}
}
