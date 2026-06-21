package virtualservices

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEcho(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?q=1", nil)
	req.Header.Set("X-Custom", "hello")
	w := httptest.NewRecorder()
	Echo()(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON content-type, got %q", ct)
	}
	var data map[string]any
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if data["method"] != "GET" {
		t.Errorf("expected method GET, got %v", data["method"])
	}
	if data["path"] != "/test" {
		t.Errorf("expected path /test, got %v", data["path"])
	}
}

func TestOK_Default(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	OK()(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "OK" {
		t.Errorf("expected body OK, got %q", w.Body.String())
	}
}

func TestOK_CustomMsg(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	OK("healthy")(w, req)

	if w.Body.String() != "healthy" {
		t.Errorf("expected body healthy, got %q", w.Body.String())
	}
}

func TestOK_EmptyArgFallsBackToDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	OK("")(w, req)

	if w.Body.String() != "OK" {
		t.Errorf("expected default OK, got %q", w.Body.String())
	}
}

func TestERR_Default(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ERR()(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ERR") {
		t.Errorf("expected ERR in body, got %q", w.Body.String())
	}
}

func TestERR_NumericCodeOnly(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ERR("503")(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), http.StatusText(503)) {
		t.Errorf("expected status text in body, got %q", w.Body.String())
	}
}

func TestERR_NumericCodeAndMsg(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ERR("418", "I'm a teapot")(w, req)

	if w.Code != 418 {
		t.Errorf("expected 418, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "I'm a teapot") {
		t.Errorf("expected message in body, got %q", w.Body.String())
	}
}

func TestERR_StringMsg(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ERR("custom error")(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "custom error") {
		t.Errorf("expected custom error in body, got %q", w.Body.String())
	}
}

func TestRedirect_TooFewArgs(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	Redirect()(w, req)

	// noop handler: no redirect headers, status stays 200
	if loc := w.Header().Get("Location"); loc != "" {
		t.Errorf("expected no Location header, got %q", loc)
	}
}

func TestRedirect_OneArg(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	Redirect("301")(w, req) // still < 2 args → noop

	if w.Header().Get("Location") != "" {
		t.Error("expected no redirect with only one arg")
	}
}

func TestRedirect_Valid(t *testing.T) {
	req := httptest.NewRequest("GET", "/old", nil)
	w := httptest.NewRecorder()
	Redirect("301", "/new")(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Errorf("expected 301, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/new" {
		t.Errorf("expected Location /new, got %q", loc)
	}
}

func TestDiscovery(t *testing.T) {
	state := map[string][]string{
		"svc-a": {"/tmp/a.sock"},
		"svc-b": {"/tmp/b.sock", "/tmp/b2.sock"},
	}
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	Discovery(state)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var got map[string][]string
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got["svc-b"]) != 2 {
		t.Errorf("expected 2 nodes for svc-b, got %d", len(got["svc-b"]))
	}
}

func TestJSON_Default(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	JSON()(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "{}" {
		t.Errorf("expected {}, got %q", w.Body.String())
	}
}

func TestJSON_CustomPayload(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	JSON(`{"key":"val"}`)(w, req)

	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON content-type, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), `"key"`) {
		t.Errorf("unexpected body: %q", w.Body.String())
	}
}

func TestPing_NoNodes(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	Ping("my-svc", nil)(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unreachable") {
		t.Errorf("expected unreachable in body, got %q", w.Body.String())
	}
}

func TestPing_Up(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ping-up-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "test.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	Ping("my-svc", []string{sockPath})(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"status": "up"`) {
		t.Errorf("expected status up in body, got %q", w.Body.String())
	}
}

func TestPing_Down(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	Ping("my-svc", []string{"/nonexistent/path.sock"})(w, req)

	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("expected 504, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "down") {
		t.Errorf("expected down in body, got %q", w.Body.String())
	}
}

func TestMetrics(t *testing.T) {
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	Metrics()(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain content-type, got %q", ct)
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
			if ok != tt.wantOk {
				t.Errorf("IsValid(%q) ok = %v, want %v", tt.expr, ok, tt.wantOk)
			}
			if name != tt.wantName {
				t.Errorf("IsValid(%q) name = %q, want %q", tt.expr, name, tt.wantName)
			}
		})
	}
}
