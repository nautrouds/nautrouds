package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
)

var _ http.ResponseWriter = (*trackedResponseWriter)(nil)
var _ io.Writer = (*trackedResponseWriter)(nil)

func newTrackedResponseWriter(w http.ResponseWriter) *trackedResponseWriter {
	return &trackedResponseWriter{ResponseWriter: w, status: http.StatusOK}
}

// trackedResponseWriter wraps http.ResponseWriter to track activity, status code, and response size.
type trackedResponseWriter struct {
	http.ResponseWriter
	status   int
	size     int64
	hijacked bool
}

func (r *trackedResponseWriter) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *trackedResponseWriter) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.size += int64(n)
	return n, err
}

func (r *trackedResponseWriter) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *trackedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		conn, rw, err := h.Hijack()
		if err == nil {
			r.hijacked = true
		}
		return conn, rw, err
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
}
