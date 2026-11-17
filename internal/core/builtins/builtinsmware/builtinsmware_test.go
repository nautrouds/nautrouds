package builtinsmware_test

import (
	"bufio"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"nautrouds/internal/core/builtins/builtinsmware"
	"nautrouds/internal/core/mmfg"
	"nautrouds/internal/core/routeoptions"
	"nautrouds/internal/core/tempresp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func basicAuthHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func TestRequestRewrite(t *testing.T) {
	tests := []struct {
		name    string
		factory builtinsmware.MiddlewareFactory
		args    []string
		url     string
		headers map[string]string
		check   func(t *testing.T, req *http.Request)
	}{
		{
			name:    "SetHeader",
			factory: builtinsmware.SetHeader,
			args:    []string{"X-Custom", "hello"},
			url:     "/",
			check: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "hello", req.Header.Get("X-Custom"))
			},
		},
		{
			name:    "SetHeader/Overwrite",
			factory: builtinsmware.SetHeader,
			args:    []string{"X-Custom", "new"},
			url:     "/",
			headers: map[string]string{"X-Custom": "old"},
			check: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "new", req.Header.Get("X-Custom"))
			},
		},
		{
			name:    "DelHeader",
			factory: builtinsmware.DelHeader,
			args:    []string{"X-Remove"},
			url:     "/",
			headers: map[string]string{"X-Remove": "value"},
			check: func(t *testing.T, req *http.Request) {
				assert.Empty(t, req.Header.Get("X-Remove"))
			},
		},
		{
			name:    "DelHeader/NonExistent",
			factory: builtinsmware.DelHeader,
			args:    []string{"X-Missing"},
			url:     "/",
			check: func(t *testing.T, req *http.Request) {
				assert.Empty(t, req.Header.Get("X-Missing"))
			},
		},
		{
			name:    "SetHost",
			factory: builtinsmware.SetHost,
			args:    []string{"override.example.com"},
			url:     "/",
			check: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "override.example.com", req.Host)
			},
		},
		{
			name:    "PathTrimPrefix/Matches",
			factory: builtinsmware.PathTrimPrefix,
			args:    []string{"/api"},
			url:     "/api/users",
			check: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "/users", req.URL.Path)
			},
		},
		{
			name:    "PathTrimPrefix/NoMatch",
			factory: builtinsmware.PathTrimPrefix,
			args:    []string{"/admin"},
			url:     "/api/users",
			check: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "/api/users", req.URL.Path)
			},
		},
		{
			name:    "PathTrimPrefix/UpdatesRequestURI",
			factory: builtinsmware.PathTrimPrefix,
			args:    []string{"/v1"},
			url:     "/v1/items?q=test",
			check: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "/items", req.URL.Path)
				assert.Equal(t, "/items?q=test", req.RequestURI)
			},
		},
		{
			name:    "RewritePath",
			factory: builtinsmware.RewritePath,
			args:    []string{"/old", "/new"},
			url:     "/old/resource",
			check: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "/new/resource", req.URL.Path)
			},
		},
		{
			name:    "RewritePath/NoMatch",
			factory: builtinsmware.RewritePath,
			args:    []string{"/old", "/new"},
			url:     "/other/resource",
			check: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "/other/resource", req.URL.Path)
			},
		},
		{
			name:    "SetQuery/AddsKey",
			factory: builtinsmware.SetQuery,
			args:    []string{"version", "2"},
			url:     "/api",
			check: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "2", req.URL.Query().Get("version"))
			},
		},
		{
			name:    "SetQuery/PreservesExisting",
			factory: builtinsmware.SetQuery,
			args:    []string{"version", "2"},
			url:     "/api?existing=true",
			check: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "2", req.URL.Query().Get("version"))
				assert.Equal(t, "true", req.URL.Query().Get("existing"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, err := tt.factory(tt.args...)
			require.NoError(t, err)

			req := httptest.NewRequest("GET", tt.url, nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			w, _ := newWriter()

			fn(w, req, nil, nil)

			assert.Equal(t, http.StatusOK, w.GetCode())
			tt.check(t, req)
		})
	}
}

func TestAccessControl(t *testing.T) {
	tests := []struct {
		name        string
		factory     builtinsmware.MiddlewareFactory
		args        []string
		headers     map[string]string
		remoteAddr  string
		mmfgHeaders map[string]string
		wantCode    int
	}{
		{
			name:     "BasicAuth/Valid",
			factory:  builtinsmware.BasicAuth,
			args:     []string{"admin", "secret"},
			headers:  map[string]string{"Authorization": basicAuthHeader("admin", "secret")},
			wantCode: http.StatusOK,
		},
		{
			name:     "BasicAuth/WrongPassword",
			factory:  builtinsmware.BasicAuth,
			args:     []string{"admin", "secret"},
			headers:  map[string]string{"Authorization": basicAuthHeader("admin", "wrong")},
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "BasicAuth/NoHeader",
			factory:  builtinsmware.BasicAuth,
			args:     []string{"admin", "secret"},
			wantCode: http.StatusUnauthorized,
		},
		{
			name:        "BasicAuth/Mmfg/Valid",
			factory:     builtinsmware.BasicAuth,
			args:        []string{"admin", "secret"},
			mmfgHeaders: map[string]string{"Authorization": basicAuthHeader("admin", "secret")},
			wantCode:    http.StatusOK,
		},
		{
			name:        "BasicAuth/Mmfg/WrongPassword",
			factory:     builtinsmware.BasicAuth,
			args:        []string{"admin", "secret"},
			mmfgHeaders: map[string]string{"Authorization": basicAuthHeader("admin", "wrong")},
			wantCode:    http.StatusUnauthorized,
		},
		{
			name:        "BasicAuth/Mmfg/NoHeader",
			factory:     builtinsmware.BasicAuth,
			args:        []string{"admin", "secret"},
			mmfgHeaders: map[string]string{},
			wantCode:    http.StatusUnauthorized,
		},
		{
			name:     "RequireHeader/Match",
			factory:  builtinsmware.RequireHeader,
			args:     []string{"X-Internal", "yes"},
			headers:  map[string]string{"X-Internal": "yes"},
			wantCode: http.StatusOK,
		},
		{
			name:     "RequireHeader/Mismatch",
			factory:  builtinsmware.RequireHeader,
			args:     []string{"X-Internal", "yes"},
			headers:  map[string]string{"X-Internal": "no"},
			wantCode: http.StatusForbidden,
		},
		{
			name:     "RequireHeader/Missing",
			factory:  builtinsmware.RequireHeader,
			args:     []string{"X-Internal", "yes"},
			wantCode: http.StatusForbidden,
		},
		{
			name:        "RequireHeader/Mmfg/Match",
			factory:     builtinsmware.RequireHeader,
			args:        []string{"X-Internal", "yes"},
			mmfgHeaders: map[string]string{"X-Internal": "yes"},
			wantCode:    http.StatusOK,
		},
		{
			name:        "RequireHeader/Mmfg/Missing",
			factory:     builtinsmware.RequireHeader,
			args:        []string{"X-Internal", "yes"},
			mmfgHeaders: map[string]string{},
			wantCode:    http.StatusForbidden,
		},
		{
			name:     "IPAllow/Allowed",
			factory:  builtinsmware.IPAllow,
			args:     []string{"192.0.2.0/24"},
			headers:  map[string]string{"X-Forwarded-For": "192.0.2.1"},
			wantCode: http.StatusOK,
		},
		{
			name:     "IPAllow/Blocked",
			factory:  builtinsmware.IPAllow,
			args:     []string{"10.0.0.0/8"},
			headers:  map[string]string{"X-Forwarded-For": "192.0.2.1"},
			wantCode: http.StatusForbidden,
		},
		{
			name:       "IPAllow/HeaderKey/Allowed",
			factory:    builtinsmware.IPAllow,
			args:       []string{"X-Real-IP", "192.0.2.0/24"},
			headers:    map[string]string{"X-Real-IP": "192.0.2.1"},
			remoteAddr: "10.0.0.1:1234",
			wantCode:   http.StatusOK,
		},
		{
			name:       "IPAllow/HeaderKey/Blocked",
			factory:    builtinsmware.IPAllow,
			args:       []string{"X-Real-IP", "192.0.2.0/24"},
			headers:    map[string]string{"X-Real-IP": "10.0.0.1"},
			remoteAddr: "192.0.2.1:1234",
			wantCode:   http.StatusForbidden,
		},
		{
			name:        "IPAllow/HeaderKey/Mmfg/Allowed",
			factory:     builtinsmware.IPAllow,
			args:        []string{"X-Real-IP", "192.0.2.0/24"},
			mmfgHeaders: map[string]string{"X-Real-IP": "192.0.2.1"},
			wantCode:    http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, err := tt.factory(tt.args...)
			require.NoError(t, err)

			req := httptest.NewRequest("GET", "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			if tt.remoteAddr != "" {
				req.RemoteAddr = tt.remoteAddr
			}

			var mr mmfg.Request
			if tt.mmfgHeaders != nil {
				mr = &fakeMmfgRequest{headers: tt.mmfgHeaders}
			}
			w, _ := newWriter()

			fn(w, req, mr, nil)

			assert.Equal(t, tt.wantCode, w.GetCode())
		})
	}
}

func TestBasicAuth_SetsWWWAuthenticate(t *testing.T) {
	fn, err := builtinsmware.BasicAuth("admin", "secret")
	require.NoError(t, err)

	w, rec := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	fn(w, req, nil, nil)
	w.Commit()

	assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Basic")
}

func TestLog_PrintsLineAsIs(t *testing.T) {
	fn, err := builtinsmware.Log("hit GET /path from 127.0.0.1")
	require.NoError(t, err)
	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/path", nil)
	req.RemoteAddr = "127.0.0.1:9999"

	r, wPipe, err := os.Pipe()
	require.NoError(t, err)
	origStdout := os.Stdout
	os.Stdout = wPipe
	fn(w, req, nil, nil)
	wPipe.Close()
	os.Stdout = origStdout

	scanner := bufio.NewScanner(r)
	require.True(t, scanner.Scan())
	assert.Equal(t, "hit GET /path from 127.0.0.1", scanner.Text())
}

func TestBodySizeLimit_ParsesSizes(t *testing.T) {
	tests := []struct {
		arg     string
		wantMax int64
		wantErr bool
	}{
		{"1024", 1024, false},
		{"10KB", 10 * 1024, false},
		{"5MB", 5 * 1024 * 1024, false},
		{"1GB", 1024 * 1024 * 1024, false},
		{"1.5MB", int64(1.5 * 1024 * 1024), false},
		{"", 0, true},
		{"-1", 0, true},
		{"not-a-size", 0, true},
		{"9999999999999999999GB", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			fn, err := builtinsmware.BodySizeLimit(tt.arg)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			w, _ := newWriter()
			req := httptest.NewRequest("POST", "/", strings.NewReader(""))
			req.ContentLength = tt.wantMax + 1
			fn(w, req, nil, nil)
			assert.Equal(t, http.StatusRequestEntityTooLarge, w.GetCode())
		})
	}
}

func TestBodySizeLimit_RejectsOversizedContentLength(t *testing.T) {
	fn, err := builtinsmware.BodySizeLimit("10")
	require.NoError(t, err)
	w, _ := newWriter()
	req := httptest.NewRequest("POST", "/", strings.NewReader("this body is way over ten bytes"))
	req.ContentLength = 32
	fn(w, req, nil, nil)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.GetCode())
}

func TestBodySizeLimit_WrapsBodyWithinLimit(t *testing.T) {
	fn, err := builtinsmware.BodySizeLimit("1KB")
	require.NoError(t, err)
	w, _ := newWriter()
	req := httptest.NewRequest("POST", "/", strings.NewReader("small body"))
	req.ContentLength = int64(len("small body"))
	fn(w, req, nil, nil)
	assert.Equal(t, http.StatusOK, w.GetCode())

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, "small body", string(body))
}

func TestBodySizeLimit_TruncatesOversizedChunkedBody(t *testing.T) {
	fn, err := builtinsmware.BodySizeLimit("10")
	require.NoError(t, err)
	w, _ := newWriter()
	req := httptest.NewRequest("POST", "/", strings.NewReader("this body is way over ten bytes"))
	req.ContentLength = -1
	fn(w, req, nil, nil)
	assert.Equal(t, http.StatusOK, w.GetCode())

	_, err = io.ReadAll(req.Body)
	assert.Error(t, err)
}

func TestRetryLimit_SetsOptionOnOptions(t *testing.T) {
	fn, err := builtinsmware.RetryLimit("3")
	require.NoError(t, err)

	w, _ := newWriter()
	req := httptest.NewRequest("GET", "/", nil)
	o := &routeoptions.Options{}
	fn(w, req, nil, o)

	assert.True(t, o.HasRetryLimit)
	assert.EqualValues(t, 3, o.RetryLimit)
}

func TestBuild_Errors(t *testing.T) {
	tests := []struct {
		name    string
		factory builtinsmware.MiddlewareFactory
		args    []string
	}{
		{"SetHeader/TooFew", builtinsmware.SetHeader, []string{"a"}},
		{"SetHeader/TooMany", builtinsmware.SetHeader, []string{"a", "b", "c"}},
		{"DelHeader/TooFew", builtinsmware.DelHeader, nil},
		{"DelHeader/TooMany", builtinsmware.DelHeader, []string{"a", "b"}},
		{"SetHost/TooFew", builtinsmware.SetHost, nil},
		{"PathTrimPrefix/TooMany", builtinsmware.PathTrimPrefix, []string{"a", "b"}},
		{"RewritePath/TooFew", builtinsmware.RewritePath, []string{"a"}},
		{"SetQuery/TooFew", builtinsmware.SetQuery, []string{"a"}},
		{"BasicAuth/TooFew", builtinsmware.BasicAuth, []string{"a"}},
		{"RequireHeader/TooMany", builtinsmware.RequireHeader, []string{"a", "b", "c"}},
		{"IPAllow/TooFew", builtinsmware.IPAllow, nil},
		{"IPAllow/TooMany", builtinsmware.IPAllow, []string{"a", "b", "c"}},
		{"IPAllow/InvalidCIDR", builtinsmware.IPAllow, []string{"not-a-cidr"}},
		{"Log/TooFew", builtinsmware.Log, nil},
		{"BodySizeLimit/TooFew", builtinsmware.BodySizeLimit, nil},
		{"BodySizeLimit/TooMany", builtinsmware.BodySizeLimit, []string{"1", "2"}},
		{"RetryLimit/TooFew", builtinsmware.RetryLimit, nil},
		{"RetryLimit/Empty", builtinsmware.RetryLimit, []string{""}},
		{"RetryLimit/Negative", builtinsmware.RetryLimit, []string{"-1"}},
		{"RetryLimit/NotANumber", builtinsmware.RetryLimit, []string{"not-a-number"}},
		{"RetryLimit/Fraction", builtinsmware.RetryLimit, []string{"1.5"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.factory(tt.args...)
			assert.Error(t, err)
		})
	}
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
		{"$BodySizeLimit(10MB)", true},
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
