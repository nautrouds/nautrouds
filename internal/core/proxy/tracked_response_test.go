package proxy

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackedResponseWriter_DefaultStatus(t *testing.T) {
	w := newTrackedResponseWriter(httptest.NewRecorder())
	assert.Equal(t, http.StatusOK, w.status)
	assert.Zero(t, w.size)
}

func TestTrackedResponseWriter_WriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newTrackedResponseWriter(rec)
	w.WriteHeader(http.StatusNotFound)

	assert.Equal(t, http.StatusNotFound, w.status)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTrackedResponseWriter_WriteAccumulates(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newTrackedResponseWriter(rec)

	n, err := w.Write([]byte("ab"))
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	w.Write([]byte("cde"))

	assert.EqualValues(t, 5, w.size)
	assert.Equal(t, "abcde", rec.Body.String())
}

func TestTrackedResponseWriter_Flush_WithFlusher(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newTrackedResponseWriter(rec)
	w.Flush()

	assert.True(t, rec.Flushed)
}

type noopResponseWriter struct{ http.ResponseWriter }

func TestTrackedResponseWriter_Flush_WithoutFlusher(t *testing.T) {
	w := newTrackedResponseWriter(&noopResponseWriter{httptest.NewRecorder()})
	assert.NotPanics(t, w.Flush)
}

func TestTrackedResponseWriter_Hijack_WithoutSupport(t *testing.T) {
	w := newTrackedResponseWriter(httptest.NewRecorder())
	_, _, err := w.Hijack()
	assert.Error(t, err)
}

type hijackWriter struct {
	http.ResponseWriter
	conn net.Conn
}

func (h *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	reader := bufio.NewReader(h.conn)
	writer := bufio.NewWriter(h.conn)
	return h.conn, bufio.NewReadWriter(reader, writer), nil
}

func TestTrackedResponseWriter_Hijack_WithSupport(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	w := newTrackedResponseWriter(&hijackWriter{ResponseWriter: httptest.NewRecorder(), conn: server})

	conn, _, err := w.Hijack()
	require.NoError(t, err)
	assert.Same(t, server, conn)
}
