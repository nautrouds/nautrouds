package virtualservices

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEcho(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?q=1", nil)
	req.Header.Set("X-Custom", "hello")
	w := httptest.NewRecorder()
	h, err := Echo()
	require.NoError(t, err)
	h(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	var data map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&data))
	assert.Equal(t, "GET", data["method"])
	assert.Equal(t, "/test", data["path"])
}

func TestDiscovery(t *testing.T) {
	state := map[string][]string{
		"svc-a": {"/tmp/a.sock"},
		"svc-b": {"/tmp/b.sock", "/tmp/b2.sock"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	Discovery(state)(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var got map[string][]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	assert.Equal(t, state, got)
}

func TestHandlers(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	ln, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	defer ln.Close()

	tests := []struct {
		name            string
		build           func() (http.HandlerFunc, error)
		wantCode        int
		wantBody        string
		wantContentType string
		wantLocation    string
	}{
		{
			name:     "OK/Default",
			build:    func() (http.HandlerFunc, error) { return OK() },
			wantCode: http.StatusOK,
			wantBody: "OK",
		},
		{
			name:     "OK/CustomMsg",
			build:    func() (http.HandlerFunc, error) { return OK("healthy") },
			wantCode: http.StatusOK,
			wantBody: "healthy",
		},
		{
			name:     "OK/EmptyArgFallsBackToDefault",
			build:    func() (http.HandlerFunc, error) { return OK("") },
			wantCode: http.StatusOK,
			wantBody: "OK",
		},
		{
			name:     "ERR/Default",
			build:    func() (http.HandlerFunc, error) { return ERR() },
			wantCode: http.StatusBadRequest,
			wantBody: "ERR",
		},
		{
			name:     "ERR/NumericCodeOnly",
			build:    func() (http.HandlerFunc, error) { return ERR("503") },
			wantCode: http.StatusServiceUnavailable,
			wantBody: http.StatusText(503),
		},
		{
			name:     "ERR/NumericCodeAndMsg",
			build:    func() (http.HandlerFunc, error) { return ERR("418", "I'm a teapot") },
			wantCode: 418,
			wantBody: "I'm a teapot",
		},
		{
			name:     "ERR/StringMsg",
			build:    func() (http.HandlerFunc, error) { return ERR("custom error") },
			wantCode: http.StatusBadRequest,
			wantBody: "custom error",
		},
		{
			name:         "Redirect/Valid",
			build:        func() (http.HandlerFunc, error) { return Redirect("301", "/new") },
			wantCode:     http.StatusMovedPermanently,
			wantLocation: "/new",
		},
		{
			name:     "JSON/Default",
			build:    func() (http.HandlerFunc, error) { return JSON() },
			wantCode: http.StatusOK,
			wantBody: "{}",
		},
		{
			name:            "JSON/CustomPayload",
			build:           func() (http.HandlerFunc, error) { return JSON(`{"key":"val"}`) },
			wantCode:        http.StatusOK,
			wantBody:        `"key"`,
			wantContentType: "application/json",
		},
		{
			name:            "Welcome",
			build:           func() (http.HandlerFunc, error) { return Welcome() },
			wantCode:        http.StatusOK,
			wantBody:        "Nautrouds",
			wantContentType: "text/html",
		},
		{
			name:            "Metrics",
			build:           func() (http.HandlerFunc, error) { return Metrics() },
			wantCode:        http.StatusOK,
			wantContentType: "text/plain",
		},
		{
			name:     "Ping/NoNodes",
			build:    func() (http.HandlerFunc, error) { return Ping("my-svc", nil), nil },
			wantCode: http.StatusServiceUnavailable,
			wantBody: "unreachable",
		},
		{
			name:     "Ping/Up",
			build:    func() (http.HandlerFunc, error) { return Ping("my-svc", []string{sockPath}), nil },
			wantCode: http.StatusOK,
			wantBody: `"status": "up"`,
		},
		{
			name:     "Ping/Down",
			build:    func() (http.HandlerFunc, error) { return Ping("my-svc", []string{"/nonexistent/path.sock"}), nil },
			wantCode: http.StatusGatewayTimeout,
			wantBody: "down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := tt.build()
			require.NoError(t, err)
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/", nil)
			h(w, req)

			assert.Equal(t, tt.wantCode, w.Code)
			assert.Contains(t, w.Body.String(), tt.wantBody)
			assert.Contains(t, w.Header().Get("Content-Type"), tt.wantContentType)
			assert.Equal(t, tt.wantLocation, w.Header().Get("Location"))
		})
	}
}

func TestBuild_Errors(t *testing.T) {
	tests := []struct {
		name string
		call func() error
	}{
		{"Echo/TooMany", func() error { _, err := Echo("x"); return err }},
		{"OK/TooMany", func() error { _, err := OK("a", "b"); return err }},
		{"ERR/TooMany", func() error { _, err := ERR("a", "b", "c"); return err }},
		{"Welcome/TooMany", func() error { _, err := Welcome("x"); return err }},
		{"Metrics/TooMany", func() error { _, err := Metrics("x"); return err }},
		{"Redirect/NoArgs", func() error { _, err := Redirect(); return err }},
		{"Redirect/TooFew", func() error { _, err := Redirect("301"); return err }},
		{"Redirect/TooMany", func() error { _, err := Redirect("301", "/new", "extra"); return err }},
		{"Redirect/InvalidCode", func() error { _, err := Redirect("abc", "/new"); return err }},
		{"Redirect/CodeTooLow", func() error { _, err := Redirect("0", "/new"); return err }},
		{"Redirect/CodeTooHigh", func() error { _, err := Redirect("1000", "/new"); return err }},
		{"JSON/TooMany", func() error { _, err := JSON("a", "b"); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Error(t, tt.call())
		})
	}
}

func TestIsValid(t *testing.T) {
	tests := []struct {
		expr     string
		wantOk   bool
		wantName string
	}{
		{"$echo", true, ""},
		{"$ok", true, ""},
		{"$ok(hello)", true, ""},
		{"$err(404)", true, ""},
		{"$welcome", true, ""},
		{"$metrics", true, ""},
		{"$redirect(301, /new)", true, ""},
		{"$json", true, ""},
		{"$unknown", false, "$unknown"},
		{"notabuiltin", false, ""},
		{"$ok(missing-close", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			ok, name := IsValid(tt.expr)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.wantName, name)
		})
	}
}
