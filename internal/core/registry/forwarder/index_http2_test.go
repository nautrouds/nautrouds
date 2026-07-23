package forwarder

import (
	"context"
	"io"
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

func TestForwarder_UsesHTTP2WhenBackendSupportsIt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-fwd-h2-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "h2.sock")
	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer l.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor != 2 {
				http.Error(w, "expected HTTP/2", http.StatusHTTPVersionNotSupported)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("h2 ok"))
		}),
	}
	server.Protocols = new(http.Protocols)
	server.Protocols.SetUnencryptedHTTP2(true)
	go server.Serve(l)
	defer server.Shutdown(context.Background())

	onFailure := make(chan FailureForwarder, 1)
	f := New("test-service", socketPath, onFailure)

	require.Eventually(t, func() bool {
		return f.useHTTP2.Load()
	}, 2*time.Second, 10*time.Millisecond, "expected probe to detect HTTP/2 support")

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()
	err = f.Forward(w, req)
	require.NoError(t, err)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "h2 ok", string(body))
}

func TestForwarder_FallsBackToHTTP1WhenBackendDoesNotSupportH2(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-fwd-h1-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "h1.sock")
	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer l.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("h1 ok"))
		}),
	}
	go server.Serve(l)
	defer server.Shutdown(context.Background())

	onFailure := make(chan FailureForwarder, 1)
	f := New("test-service", socketPath, onFailure)

	// let the background probe settle before checking it resolved to false
	time.Sleep(300 * time.Millisecond)
	assert.False(t, f.useHTTP2.Load())

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()
	err = f.Forward(w, req)
	require.NoError(t, err)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "h1 ok", string(body))
}

func TestForwarder_TryReconnectReprobesHTTP2(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-fwd-reprobe-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "reprobe.sock")

	l1, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	h1Server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	go h1Server.Serve(l1)

	onFailure := make(chan FailureForwarder, 1)
	f := New("test-service", socketPath, onFailure)

	require.Eventually(t, func() bool {
		return !f.useHTTP2.Load()
	}, 2*time.Second, 10*time.Millisecond, "expected probe to settle on HTTP/1.1")

	h1Server.Close()
	l1.Close()
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		require.NoError(t, err)
	}

	l2, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer l2.Close()

	h2Server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor != 2 {
				http.Error(w, "expected HTTP/2", http.StatusHTTPVersionNotSupported)
				return
			}
			w.WriteHeader(http.StatusOK)
		}),
	}
	h2Server.Protocols = new(http.Protocols)
	h2Server.Protocols.SetUnencryptedHTTP2(true)
	go h2Server.Serve(l2)
	defer h2Server.Shutdown(context.Background())

	err = f.TryReconnect()
	require.NoError(t, err)
	assert.True(t, f.useHTTP2.Load(), "TryReconnect should synchronously re-probe before returning")

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()
	err = f.Forward(w, req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
}
