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
	h, err := Echo()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h(w, req)

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
	h, err := OK()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h(w, req)

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
	h, err := OK("healthy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h(w, req)

	if w.Body.String() != "healthy" {
		t.Errorf("expected body healthy, got %q", w.Body.String())
	}
}

func TestOK_EmptyArgFallsBackToDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h, err := OK("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h(w, req)

	if w.Body.String() != "OK" {
		t.Errorf("expected default OK, got %q", w.Body.String())
	}
}

func TestERR_Default(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h, err := ERR()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h(w, req)

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
	h, err := ERR("503")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h(w, req)

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
	h, err := ERR("418", "I'm a teapot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h(w, req)

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
	h, err := ERR("custom error")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "custom error") {
		t.Errorf("expected custom error in body, got %q", w.Body.String())
	}
}

func TestRedirect_TooFewArgs(t *testing.T) {
	if _, err := Redirect(); err == nil {
		t.Error("expected error for 0 arguments")
	}
}

func TestRedirect_OneArg(t *testing.T) {
	if _, err := Redirect("301"); err == nil {
		t.Error("expected error for 1 argument")
	}
}

func TestRedirect_Valid(t *testing.T) {
	req := httptest.NewRequest("GET", "/old", nil)
	w := httptest.NewRecorder()
	h, err := Redirect("301", "/new")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h(w, req)

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
	h, err := JSON()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h(w, req)

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
	h, err := JSON(`{"key":"val"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h(w, req)

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
	h, err := Metrics()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain content-type, got %q", ct)
	}
}

func TestArgCount_Errors(t *testing.T) {
	tests := []struct {
		name string
		call func() error
	}{
		{"Echo/TooMany", func() error { _, err := Echo("x"); return err }},
		{"OK/TooMany", func() error { _, err := OK("a", "b"); return err }},
		{"ERR/TooMany", func() error { _, err := ERR("a", "b", "c"); return err }},
		{"Metrics/TooMany", func() error { _, err := Metrics("x"); return err }},
		{"Redirect/TooFew", func() error { _, err := Redirect("301"); return err }},
		{"Redirect/TooMany", func() error { _, err := Redirect("301", "/new", "extra"); return err }},
		{"JSON/TooMany", func() error { _, err := JSON("a", "b"); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil {
				t.Errorf("expected error, got nil")
			}
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
