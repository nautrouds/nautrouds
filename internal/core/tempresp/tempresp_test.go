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

func TestBufferedMode(t *testing.T) {
	tests := []struct {
		name       string
		act        func(w *tempresp.ResponseWriter)
		wantCode   int
		wantBody   string
		wantHeader string
	}{
		{
			name:     "DefaultStatus",
			act:      func(w *tempresp.ResponseWriter) {},
			wantCode: http.StatusOK,
		},
		{
			name:     "WriteHeader",
			act:      func(w *tempresp.ResponseWriter) { w.WriteHeader(http.StatusCreated) },
			wantCode: http.StatusCreated,
		},
		{
			name:     "Write",
			act:      func(w *tempresp.ResponseWriter) { w.Write([]byte("buffered")) },
			wantCode: http.StatusOK,
			wantBody: "buffered",
		},
		{
			name: "HeadersStatusBody",
			act: func(w *tempresp.ResponseWriter) {
				w.Header().Set("X-Custom", "value")
				w.WriteHeader(http.StatusAccepted)
				w.Write([]byte("hello"))
			},
			wantCode:   http.StatusAccepted,
			wantBody:   "hello",
			wantHeader: "value",
		},
		{
			name:     "Reply",
			act:      func(w *tempresp.ResponseWriter) { w.Reply("error message", http.StatusBadRequest) },
			wantCode: http.StatusBadRequest,
			wantBody: "error message",
		},
		{
			name: "ReplyResetsBody",
			act: func(w *tempresp.ResponseWriter) {
				w.Write([]byte("old body"))
				w.Reply("new body", http.StatusOK)
			},
			wantCode: http.StatusOK,
			wantBody: "new body",
		},
		{
			name: "ReplyReader",
			act: func(w *tempresp.ResponseWriter) {
				w.ReplyReader(strings.NewReader("streamed body"), http.StatusAccepted)
			},
			wantCode: http.StatusAccepted,
			wantBody: "streamed body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			w := getWriter(rec)
			tt.act(w)

			assert.False(t, w.IsPassthrough())
			assert.Equal(t, tt.wantCode, w.GetCode())
			assert.Empty(t, rec.Body.String())

			out := httptest.NewRecorder()
			require.NoError(t, w.WriteTo(out))
			assert.Equal(t, tt.wantCode, out.Code)
			assert.Equal(t, tt.wantBody, out.Body.String())
			assert.Equal(t, tt.wantHeader, out.Header().Get("X-Custom"))
		})
	}
}

func TestBufferedMode_Commit_WritesToDownstream(t *testing.T) {
	rec := httptest.NewRecorder()
	w := getWriter(rec)
	w.Header().Set("X-Committed", "yes")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("committed body"))

	require.NoError(t, w.Commit())
	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "committed body", rec.Body.String())
	assert.Equal(t, "yes", rec.Header().Get("X-Committed"))
}

func TestPassthroughMode_GoesToDownstream(t *testing.T) {
	tests := []struct {
		name       string
		act        func(w *tempresp.ResponseWriter)
		wantCode   int
		wantBody   string
		wantHeader string
	}{
		{"Header", func(w *tempresp.ResponseWriter) { w.Header().Set("X-Custom", "yes") }, http.StatusOK, "", "yes"},
		{"WriteHeader", func(w *tempresp.ResponseWriter) { w.WriteHeader(http.StatusTeapot) }, http.StatusTeapot, "", ""},
		{"Write", func(w *tempresp.ResponseWriter) { w.Write([]byte("direct output")) }, http.StatusOK, "direct output", ""},
		{"Reply", func(w *tempresp.ResponseWriter) { w.Reply("forbidden", http.StatusForbidden) }, http.StatusForbidden, "forbidden", ""},
		{"ReplyReader", func(w *tempresp.ResponseWriter) {
			w.ReplyReader(strings.NewReader("streamed"), http.StatusOK)
		}, http.StatusOK, "streamed", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			w := getWriter(rec)
			w.EnablePassthrough()
			require.True(t, w.IsPassthrough())

			tt.act(w)

			assert.Equal(t, tt.wantCode, rec.Code)
			assert.Equal(t, tt.wantBody, rec.Body.String())
			assert.Equal(t, tt.wantHeader, rec.Header().Get("X-Custom"))
		})
	}
}

func TestPassthroughMode_WriteTo_IsNoop(t *testing.T) {
	w := getWriter(httptest.NewRecorder())
	w.EnablePassthrough()
	w.Write([]byte("already sent to downstream"))

	out := httptest.NewRecorder()
	require.NoError(t, w.WriteTo(out))
	assert.Empty(t, out.Body.String())
}

func TestReset_ClearsAllState(t *testing.T) {
	w := getWriter(httptest.NewRecorder())
	w.Header().Set("X-Foo", "bar")
	w.WriteHeader(http.StatusTeapot)
	w.Write([]byte("data"))
	w.EnablePassthrough()

	w.Reset()

	assert.False(t, w.IsPassthrough())
	assert.Equal(t, http.StatusOK, w.GetCode())
	assert.Empty(t, w.Header())

	out := httptest.NewRecorder()
	w.WriteTo(out)
	assert.Equal(t, http.StatusOK, out.Code)
	assert.Empty(t, out.Body.String())
}

func TestPool_SetupResetsStateAfterReuse(t *testing.T) {
	w := getWriter(httptest.NewRecorder())
	w.WriteHeader(http.StatusTeapot)
	tempresp.Pool.Put(w)

	assert.Equal(t, http.StatusOK, getWriter(httptest.NewRecorder()).GetCode())
}
