package proxy

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTrackedResponseWriter_DefaultStatus(t *testing.T) {
	w := newTrackedResponseWriter(httptest.NewRecorder())
	if w.status != http.StatusOK {
		t.Errorf("default status should be 200, got %d", w.status)
	}
	if w.size != 0 {
		t.Errorf("default size should be 0, got %d", w.size)
	}
}

func TestTrackedResponseWriter_WriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newTrackedResponseWriter(rec)
	w.WriteHeader(http.StatusNotFound)

	if w.status != http.StatusNotFound {
		t.Errorf("expected tracked status 404, got %d", w.status)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected underlying recorder 404, got %d", rec.Code)
	}
}

func TestTrackedResponseWriter_Write(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newTrackedResponseWriter(rec)

	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5, got %d", n)
	}
	if w.size != 5 {
		t.Errorf("expected tracked size 5, got %d", w.size)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("expected body hello, got %q", rec.Body.String())
	}
}

func TestTrackedResponseWriter_WriteAccumulates(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newTrackedResponseWriter(rec)
	w.Write([]byte("ab"))
	w.Write([]byte("cde"))

	if w.size != 5 {
		t.Errorf("expected accumulated size 5, got %d", w.size)
	}
}

func TestTrackedResponseWriter_Flush_WithFlusher(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newTrackedResponseWriter(rec)
	w.Flush()

	if !rec.Flushed {
		t.Error("expected underlying flusher to be called")
	}
}

type noopResponseWriter struct{ http.ResponseWriter }

func TestTrackedResponseWriter_Flush_WithoutFlusher(t *testing.T) {
	noop := &noopResponseWriter{httptest.NewRecorder()}
	w := newTrackedResponseWriter(noop)
	w.Flush() // should not panic
}

func TestTrackedResponseWriter_Hijack_WithoutSupport(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newTrackedResponseWriter(rec)
	_, _, err := w.Hijack()
	if err == nil {
		t.Error("expected error when underlying writer does not support hijacking")
	}
}

type hijackWriter struct {
	http.ResponseWriter
	conn net.Conn
}

func (h *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return h.conn, bufio.NewReadWriter(bufio.NewReader(h.conn), bufio.NewWriter(h.conn)), nil
}

func TestTrackedResponseWriter_Hijack_WithSupport(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	hw := &hijackWriter{ResponseWriter: httptest.NewRecorder(), conn: server}
	w := newTrackedResponseWriter(hw)

	conn, _, err := w.Hijack()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn != server {
		t.Error("expected the server-side connection to be returned")
	}
}
