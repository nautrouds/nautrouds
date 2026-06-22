package tempresp_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nautrouds/internal/core/tempresp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getWriter(downstream http.ResponseWriter) *tempresp.ResponseWriter {
	w := tempresp.Pool.Get().(*tempresp.ResponseWriter)
	w.Setup(downstream)
	return w
}

// --- Buffered mode ---

func TestBufferedMode_DefaultStatus(t *testing.T) {
	w := getWriter(httptest.NewRecorder())
	assert.Equal(t, http.StatusOK, w.GetCode())
	assert.False(t, w.IsPassthrough())
}

func TestBufferedMode_WriteHeader(t *testing.T) {
	w := getWriter(httptest.NewRecorder())
	w.WriteHeader(http.StatusCreated)
	assert.Equal(t, http.StatusCreated, w.GetCode())
}

func TestBufferedMode_Write_DoesNotFlushToDownstream(t *testing.T) {
	rec := httptest.NewRecorder()
	w := getWriter(rec)
	w.Write([]byte("buffered"))
	// Nothing written to downstream yet
	assert.Empty(t, rec.Body.String())
}

func TestBufferedMode_WriteTo_FlushesHeadersStatusBody(t *testing.T) {
	w := getWriter(httptest.NewRecorder())
	w.Header().Set("X-Custom", "value")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("hello"))

	out := httptest.NewRecorder()
	err := w.WriteTo(out)
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, out.Code)
	assert.Equal(t, "hello", out.Body.String())
	assert.Equal(t, "value", out.Header().Get("X-Custom"))
}

func TestBufferedMode_Reply(t *testing.T) {
	w := getWriter(httptest.NewRecorder())
	w.Reply("error message", http.StatusBadRequest)
	assert.Equal(t, http.StatusBadRequest, w.GetCode())

	out := httptest.NewRecorder()
	w.WriteTo(out)
	assert.Equal(t, "error message", out.Body.String())
}

func TestBufferedMode_ReplyResetsBody(t *testing.T) {
	w := getWriter(httptest.NewRecorder())
	w.Write([]byte("old body"))
	w.Reply("new body", http.StatusOK)

	out := httptest.NewRecorder()
	w.WriteTo(out)
	assert.Equal(t, "new body", out.Body.String())
}

func TestBufferedMode_ReplyReader(t *testing.T) {
	w := getWriter(httptest.NewRecorder())
	w.ReplyReader(strings.NewReader("streamed body"), http.StatusAccepted)
	assert.Equal(t, http.StatusAccepted, w.GetCode())

	out := httptest.NewRecorder()
	w.WriteTo(out)
	assert.Equal(t, "streamed body", out.Body.String())
}

func TestBufferedMode_Commit_WritesToDownstream(t *testing.T) {
	rec := httptest.NewRecorder()
	w := getWriter(rec)
	w.Header().Set("X-Committed", "yes")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("committed body"))

	err := w.Commit()
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "committed body", rec.Body.String())
	assert.Equal(t, "yes", rec.Header().Get("X-Committed"))
}

// --- Passthrough mode ---

func TestPassthroughMode_Header_ReturnsDownstreamHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	w := getWriter(rec)
	w.EnablePassthrough()
	assert.True(t, w.IsPassthrough())

	w.Header().Set("X-Passthrough", "yes")
	assert.Equal(t, "yes", rec.Header().Get("X-Passthrough"))
}

func TestPassthroughMode_WriteHeader_GoesToDownstream(t *testing.T) {
	rec := httptest.NewRecorder()
	w := getWriter(rec)
	w.EnablePassthrough()
	w.WriteHeader(http.StatusTeapot)
	assert.Equal(t, http.StatusTeapot, rec.Code)
}

func TestPassthroughMode_Write_GoesToDownstream(t *testing.T) {
	rec := httptest.NewRecorder()
	w := getWriter(rec)
	w.EnablePassthrough()
	w.Write([]byte("direct output"))
	assert.Equal(t, "direct output", rec.Body.String())
}

func TestPassthroughMode_Reply_GoesToDownstream(t *testing.T) {
	rec := httptest.NewRecorder()
	w := getWriter(rec)
	w.EnablePassthrough()
	w.Reply("forbidden", http.StatusForbidden)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "forbidden", rec.Body.String())
}

func TestPassthroughMode_WriteTo_IsNoop(t *testing.T) {
	rec := httptest.NewRecorder()
	w := getWriter(rec)
	w.EnablePassthrough()
	w.Write([]byte("already sent to downstream"))

	out := httptest.NewRecorder()
	err := w.WriteTo(out)
	require.NoError(t, err)
	// WriteTo in passthrough mode is a no-op: nothing extra written to out
	assert.Empty(t, out.Body.String())
}

func TestPassthroughMode_ReplyReader_GoesToDownstream(t *testing.T) {
	rec := httptest.NewRecorder()
	w := getWriter(rec)
	w.EnablePassthrough()
	w.ReplyReader(strings.NewReader("streamed"), http.StatusOK)
	assert.Equal(t, "streamed", rec.Body.String())
}

// --- Reset ---

func TestReset_ClearsAllState(t *testing.T) {
	rec := httptest.NewRecorder()
	w := getWriter(rec)
	w.Header().Set("X-Foo", "bar")
	w.WriteHeader(http.StatusTeapot)
	w.Write([]byte("data"))
	w.EnablePassthrough()

	w.Reset()

	assert.False(t, w.IsPassthrough())
	assert.Equal(t, http.StatusOK, w.GetCode())
	assert.Empty(t, w.Header())

	// WriteTo after reset should write empty 200 response
	out := httptest.NewRecorder()
	w.WriteTo(out)
	assert.Equal(t, http.StatusOK, out.Code)
	assert.Empty(t, out.Body.String())
}

// --- Pool ---

func TestPool_ReturnsUsableWriter(t *testing.T) {
	for i := 0; i < 5; i++ {
		w := tempresp.Pool.Get().(*tempresp.ResponseWriter)
		w.Setup(httptest.NewRecorder())
		w.WriteHeader(http.StatusTeapot)
		assert.Equal(t, http.StatusTeapot, w.GetCode())
		tempresp.Pool.Put(w)
	}
}

func TestPool_SetupResetsStateAfterReuse(t *testing.T) {
	w := tempresp.Pool.Get().(*tempresp.ResponseWriter)
	w.Setup(httptest.NewRecorder())
	w.WriteHeader(http.StatusTeapot)
	tempresp.Pool.Put(w)

	// After Put+Get, Setup must reset state regardless of whether
	// the pool returns the same object or a fresh one.
	w2 := tempresp.Pool.Get().(*tempresp.ResponseWriter)
	w2.Setup(httptest.NewRecorder())
	assert.Equal(t, http.StatusOK, w2.GetCode())
}
