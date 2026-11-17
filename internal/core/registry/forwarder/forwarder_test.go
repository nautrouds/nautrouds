package forwarder

import (
	"context"
	"errors"
	"io"
	"nautrouds/internal/core/tempresp"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const chunkedContentLength = -1

func serveUnix(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	server := &http.Server{Handler: handler}
	go server.Serve(l)
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	return socketPath
}

func forward(f *Forwarder) error {
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	return f.Forward(httptest.NewRecorder(), req)
}

func forwardMiddleware(f *Forwarder) error {
	w := tempresp.Pool.Get().(*tempresp.ResponseWriter)
	defer tempresp.Pool.Put(w)
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	return f.ForwardMiddleware(w, req, nil, "/", nil)
}

var forwardCalls = []struct {
	name string
	call func(f *Forwarder) error
}{
	{"Forward", forward},
	{"ForwardMiddleware", forwardMiddleware},
}

func TestForwarder_Forward(t *testing.T) {
	socketPath := serveUnix(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello from UDS"))
	})
	f := New("test-service", socketPath, 1, make(chan FailureForwarder, 1))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://example.com/api/test", nil)
	f.Forward(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Hello from UDS", w.Body.String())
}

func TestForwarder_Forward_ChunkedBodyExceedsMaxBytesReaderLimit(t *testing.T) {
	socketPath := serveUnix(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	})
	f := New("test-service", socketPath, 1, make(chan FailureForwarder, 1))

	req := httptest.NewRequest("POST", "http://example.com/upload", strings.NewReader("this body is way over the ten byte limit"))
	req.ContentLength = chunkedContentLength
	w := httptest.NewRecorder()
	req.Body = http.MaxBytesReader(w, req.Body, 10)

	assert.ErrorIs(t, f.Forward(w, req), ErrBodyTooLarge)
}

func TestForwarder_ForwardMiddleware(t *testing.T) {
	socketPath := serveUnix(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth") == "valid" {
			w.Header().Set("X-User-ID", "123")
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	f := New("test-service", socketPath, 1, make(chan FailureForwarder, 1))

	tests := []struct {
		name           string
		auth           string
		allowedHeaders []string
		wantErr        error
		wantCode       int
		wantUserID     string
	}{
		{"Authorized", "valid", []string{"X-User-ID"}, nil, http.StatusNoContent, "123"},
		{"Unauthorized", "invalid", nil, ErrMiddlewareBlocked, http.StatusUnauthorized, ""},
		{"Authorized_HeaderNotAllowlisted", "valid", []string{"X-Other"}, nil, http.StatusNoContent, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://example.com/", nil)
			req.Header.Set("X-Auth", tt.auth)
			w := tempresp.Pool.Get().(*tempresp.ResponseWriter)
			defer tempresp.Pool.Put(w)

			err := f.ForwardMiddleware(w, req, nil, "/", tt.allowedHeaders)
			assert.Equal(t, tt.wantErr, err)
			assert.Equal(t, tt.wantCode, w.GetCode())
			assert.Equal(t, tt.wantUserID, req.Header.Get("X-User-ID"))
		})
	}
}

func TestForwarder_UnreachableNode(t *testing.T) {
	for _, tt := range forwardCalls {
		t.Run(tt.name, func(t *testing.T) {
			socketPath := filepath.Join(t.TempDir(), "nonexistent.sock")
			onFailure := make(chan FailureForwarder, 1)
			f := New("test-service", socketPath, 1, onFailure)

			err := tt.call(f)
			assert.True(t, err == ErrNodeUnavailable || err == ErrNodeFailed, "got %v", err)
			assert.EqualValues(t, 0, f.InFlightWeight())

			select {
			case failure := <-onFailure:
				assert.Equal(t, socketPath, failure.SocketPath)
				assert.Error(t, failure.Error)
			case <-time.After(1 * time.Second):
				t.Fatal("timed out waiting for failure report")
			}
		})
	}
}

func TestForwarder_ReturnsErrNodeFailedWhenAlreadyFailed(t *testing.T) {
	for _, tt := range forwardCalls {
		t.Run(tt.name, func(t *testing.T) {
			socketPath := filepath.Join(t.TempDir(), "test.sock")
			f := New("test-service", socketPath, 1, make(chan FailureForwarder, 1))
			f.isFailed.Store(true)

			assert.Equal(t, ErrNodeFailed, tt.call(f))
		})
	}
}

func TestForwarder_InFlightTracksLifecycle(t *testing.T) {
	for _, tt := range forwardCalls {
		t.Run(tt.name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			// New()'s async HTTP/2 probe hits this socket too; its preface parses as method PRI, so ignore non-GET.
			socketPath := serveUnix(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					w.WriteHeader(http.StatusOK)
					return
				}
				close(started)
				<-release
				w.WriteHeader(http.StatusNoContent)
			})
			f := New("test-service", socketPath, 1, make(chan FailureForwarder, 1))

			done := make(chan struct{})
			go func() {
				tt.call(f)
				close(done)
			}()

			select {
			case <-started:
			case <-time.After(1 * time.Second):
				t.Fatal("timed out waiting for request to start")
			}
			assert.EqualValues(t, 1, f.InFlightWeight())

			close(release)

			select {
			case <-done:
			case <-time.After(1 * time.Second):
				t.Fatal("timed out waiting for request to finish")
			}
			assert.EqualValues(t, 0, f.InFlightWeight())
		})
	}
}

func TestCreateReverseProxy_ErrorHandler(t *testing.T) {
	tests := []struct {
		name    string
		op      string
		wantErr error
	}{
		{"DialOpErrorIsNodeUnavailable", "dial", ErrNodeUnavailable},
		{"NonDialOpErrorIsUpstreamFailed", "write", ErrUpstreamFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			onFailure := make(chan FailureForwarder, 1)
			var isFailed atomic.Bool
			rp := createReverseProxy("test-service", "/tmp/test.sock", http.DefaultTransport, onFailure, &isFailed)

			var capturedErr error
			ctx := context.WithValue(context.Background(), proxyErrorKey{}, &capturedErr)
			req := httptest.NewRequest("GET", "http://example.com/", nil).WithContext(ctx)

			rp.ErrorHandler(httptest.NewRecorder(), req, &net.OpError{Op: tt.op, Net: "unix", Err: errors.New("boom")})

			assert.ErrorIs(t, capturedErr, tt.wantErr)
			assert.True(t, isFailed.Load())

			select {
			case failure := <-onFailure:
				assert.Equal(t, "/tmp/test.sock", failure.SocketPath)
			default:
				t.Fatal("expected a failure to be reported")
			}
		})
	}
}

func TestForwarder_Forward_ConcurrentInFlightCount(t *testing.T) {
	const n = 5

	started := make(chan struct{}, n)
	release := make(chan struct{})
	socketPath := serveUnix(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	f := New("test-service", socketPath, 1, make(chan FailureForwarder, n))

	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { forward(f) })
	}

	for range n {
		select {
		case <-started:
		case <-time.After(1 * time.Second):
			t.Fatal("timed out waiting for a request to start")
		}
	}
	assert.EqualValues(t, n, f.InFlightWeight())

	close(release)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for all requests to finish")
	}
	assert.EqualValues(t, 0, f.InFlightWeight())
}
