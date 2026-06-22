package builtinsmware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nautrouds/internal/core/builtins/builtinsmware"
	"nautrouds/internal/core/tempresp"

	"github.com/stretchr/testify/assert"
)

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
	fn(w, req)
	assert.Equal(t, "hello", req.Header.Get("X-Custom"))
}

func TestSetHeader_Overwrite(t *testing.T) {
	fn := builtinsmware.SetHeader("X-Custom", "new")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Custom", "old")
	fn(w, req)
	assert.Equal(t, "new", req.Header.Get("X-Custom"))
}

func TestDelHeader(t *testing.T) {
	fn := builtinsmware.DelHeader("X-Remove")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Remove", "value")
	fn(w, req)
	assert.Empty(t, req.Header.Get("X-Remove"))
}

func TestDelHeader_NonExistent(t *testing.T) {
	fn := builtinsmware.DelHeader("X-Missing")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	// Should not panic when deleting a header that doesn't exist
	assert.NotPanics(t, func() { fn(w, req) })
}

func TestSetHost(t *testing.T) {
	fn := builtinsmware.SetHost("override.example.com")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req)
	assert.Equal(t, "override.example.com", req.Host)
}

func TestPathTrimPrefix_Matches(t *testing.T) {
	fn := builtinsmware.PathTrimPrefix("/api")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/api/users", nil)
	fn(w, req)
	assert.Equal(t, "/users", req.URL.Path)
}

func TestPathTrimPrefix_NoMatch(t *testing.T) {
	fn := builtinsmware.PathTrimPrefix("/admin")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/api/users", nil)
	fn(w, req)
	assert.Equal(t, "/api/users", req.URL.Path)
}

func TestPathTrimPrefix_UpdatesRequestURI(t *testing.T) {
	fn := builtinsmware.PathTrimPrefix("/v1")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/v1/items?q=test", nil)
	fn(w, req)
	assert.Equal(t, "/items", req.URL.Path)
	assert.Equal(t, "/items?q=test", req.RequestURI)
}

func TestRewritePath(t *testing.T) {
	fn := builtinsmware.RewritePath("/old", "/new")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/old/resource", nil)
	fn(w, req)
	assert.Equal(t, "/new/resource", req.URL.Path)
}

func TestRewritePath_NoMatch(t *testing.T) {
	fn := builtinsmware.RewritePath("/old", "/new")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/other/resource", nil)
	fn(w, req)
	assert.Equal(t, "/other/resource", req.URL.Path)
}

func TestSetQuery_AddsKey(t *testing.T) {
	fn := builtinsmware.SetQuery("version", "2")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/api", nil)
	fn(w, req)
	assert.Equal(t, "2", req.URL.Query().Get("version"))
}

func TestSetQuery_PreservesExisting(t *testing.T) {
	fn := builtinsmware.SetQuery("version", "2")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/api?existing=true", nil)
	fn(w, req)
	assert.Equal(t, "2", req.URL.Query().Get("version"))
	assert.Equal(t, "true", req.URL.Query().Get("existing"))
}

func TestBasicAuth_ValidCredentials(t *testing.T) {
	fn := builtinsmware.BasicAuth("admin", "secret")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "secret")
	fn(w, req)
	assert.Equal(t, http.StatusOK, w.GetCode())
}

func TestBasicAuth_WrongPassword(t *testing.T) {
	fn := builtinsmware.BasicAuth("admin", "secret")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "wrong")
	fn(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.GetCode())
}

func TestBasicAuth_NoHeader(t *testing.T) {
	fn := builtinsmware.BasicAuth("admin", "secret")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.GetCode())
}

func TestBasicAuth_SetsWWWAuthenticate(t *testing.T) {
	fn := builtinsmware.BasicAuth("admin", "secret")
	w, rec := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req)
	// Commit to recorder so we can inspect headers
	w.Commit()
	assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Basic")
}

func TestIPAllow_AllowedIP(t *testing.T) {
	fn := builtinsmware.IPAllow("192.0.2.0/24")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	fn(w, req)
	assert.Equal(t, http.StatusOK, w.GetCode())
}

func TestIPAllow_BlockedIP(t *testing.T) {
	fn := builtinsmware.IPAllow("10.0.0.0/8")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	fn(w, req)
	assert.Equal(t, http.StatusForbidden, w.GetCode())
}

func TestIPAllow_InvalidCIDR_FallsBackToInvalidMiddleware(t *testing.T) {
	fn := builtinsmware.IPAllow("not-a-cidr")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.GetCode())
}

func TestIPAllow_WrongArgCount_FallsBackToInvalidMiddleware(t *testing.T) {
	fn := builtinsmware.IPAllow("10.0.0.0/8", "extra")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.GetCode())
}

func TestLog_DoesNotPanic(t *testing.T) {
	fn := builtinsmware.Log("TEST")
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/path", nil)
	req.RemoteAddr = "127.0.0.1:9999"
	assert.NotPanics(t, func() { fn(w, req) })
}

func TestInvalidMiddleware(t *testing.T) {
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	builtinsmware.InvalidMiddleware(w, req)
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
