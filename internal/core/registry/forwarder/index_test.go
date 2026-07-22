package forwarder

import (
	"context"
	"io"
	"nautrouds/internal/core/tempresp"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwarder_Forward(t *testing.T) {
	// 1. Setup a mock UDS Server
	tmpDir, err := os.MkdirTemp("", "nautrouds-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "test.sock")

	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer l.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Hello from UDS"))
		}),
	}
	go server.Serve(l)
	defer server.Shutdown(context.Background())

	// 2. Use Forwarder to send request to UDS
	onFailure := make(chan FailureForwarder, 1)
	f := New("test-service", socketPath, onFailure)

	req := httptest.NewRequest("GET", "http://example.com/api/test", nil)
	w := httptest.NewRecorder()

	f.Forward(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Hello from UDS", string(body))
}

func TestForwarder_ForwardMiddleware(t *testing.T) {
	// 1. Setup a mock UDS Middleware Server
	tmpDir, err := os.MkdirTemp("", "nautrouds-mw-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "mw.sock")

	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer l.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Auth") == "valid" {
				w.Header().Set("X-User-ID", "123")
				w.WriteHeader(http.StatusNoContent)
			} else {
				w.WriteHeader(http.StatusUnauthorized)
			}
		}),
	}
	go server.Serve(l)
	defer server.Shutdown(context.Background())

	onFailure := make(chan FailureForwarder, 1)
	f := New("test-service", socketPath, onFailure)

	t.Run("Authorized", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/", nil)
		req.Header.Set("X-Auth", "valid")
		w := tempresp.Pool.Get().(*tempresp.ResponseWriter)
		defer tempresp.Pool.Put(w)

		err := f.ForwardMiddleware(w, req, nil, "/", []string{"X-User-ID"})
		assert.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, w.GetCode())
		assert.Equal(t, "123", req.Header.Get("X-User-ID"))
	})

	t.Run("Unauthorized", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/", nil)
		req.Header.Set("X-Auth", "invalid")
		w := tempresp.Pool.Get().(*tempresp.ResponseWriter)
		defer tempresp.Pool.Put(w)

		err := f.ForwardMiddleware(w, req, nil, "/", nil)
		assert.Equal(t, ErrMiddlewareBlocked, err)
		assert.Equal(t, http.StatusUnauthorized, w.GetCode())
	})

	t.Run("Authorized_HeaderNotAllowlisted", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/", nil)
		req.Header.Set("X-Auth", "valid")
		w := tempresp.Pool.Get().(*tempresp.ResponseWriter)
		defer tempresp.Pool.Put(w)

		err := f.ForwardMiddleware(w, req, nil, "/", []string{"X-Other"})
		assert.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, w.GetCode())
		assert.Empty(t, req.Header.Get("X-User-ID"))
	})
}

func TestForwarder_FailureReporting(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-fail-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "nonexistent.sock")

	onFailure := make(chan FailureForwarder, 1)
	f := New("test-service", socketPath, onFailure)

	t.Run("Forward Failure", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/", nil)
		w := httptest.NewRecorder()

		err := f.Forward(w, req)
		assert.Equal(t, ErrNodeUnavailable, err)

		select {
		case failure := <-onFailure:
			assert.Equal(t, socketPath, failure.SocketPath)
			assert.Error(t, failure.Error)
		case <-time.After(1 * time.Second):
			t.Fatal("timed out waiting for failure report")
		}
	})

	f.isFailed.Store(false)

	t.Run("ForwardMiddleware Failure", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/", nil)
		w := tempresp.Pool.Get().(*tempresp.ResponseWriter)
		defer tempresp.Pool.Put(w)

		err := f.ForwardMiddleware(w, req, nil, "/", nil)
		assert.Equal(t, ErrNodeUnavailable, err)

		select {
		case failure := <-onFailure:
			assert.Equal(t, socketPath, failure.SocketPath)
			assert.Error(t, failure.Error)
		case <-time.After(1 * time.Second):
			t.Fatal("timed out waiting for failure report")
		}
	})
}
