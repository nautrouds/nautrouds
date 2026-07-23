package forwarder

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeHTTP2_Supported(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-h2probe-supported-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "supported.sock")
	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer l.Close()

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}
	server.Protocols = new(http.Protocols)
	server.Protocols.SetHTTP1(true)
	server.Protocols.SetUnencryptedHTTP2(true)
	go server.Serve(l)

	useHTTP2, reason := probeHTTP2(socketPath)
	assert.True(t, useHTTP2)
	assert.Equal(t, reasonSupported, reason)
}

func TestProbeHTTP2_Unsupported(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-h2probe-unsupported-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "unsupported.sock")
	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer l.Close()

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}
	go server.Serve(l)

	useHTTP2, reason := probeHTTP2(socketPath)
	assert.False(t, useHTTP2)
	assert.Equal(t, reasonUnsupported, reason)
}

func TestProbeHTTP2_UnsupportedRawReset(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-h2probe-reset-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "reset.sock")
	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer l.Close()

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	useHTTP2, reason := probeHTTP2(socketPath)
	assert.False(t, useHTTP2)
	assert.Equal(t, reasonUnsupported, reason)
}

func TestProbeHTTP2_Declined(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-h2probe-declined-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "declined.sock")
	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer l.Close()

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 64)
		conn.Read(buf) // drain the preface + SETTINGS the probe sent

		// GOAWAY: length=8, type=0x7, stream 0, last-stream-id=0, error=0x0d (HTTP_1_1_REQUIRED).
		goAway := []byte{
			0x00, 0x00, 0x08,
			0x07,
			0x00,
			0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x0d,
		}
		conn.Write(goAway)
	}()

	useHTTP2, reason := probeHTTP2(socketPath)
	assert.False(t, useHTTP2)
	assert.Equal(t, reasonDeclined, reason)
}

func TestProbeHTTP2_Timeout(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-h2probe-timeout-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "timeout.sock")
	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer l.Close()

	connCh := make(chan net.Conn, 1)
	go func() {
		conn, err := l.Accept()
		if err == nil {
			connCh <- conn
		}
	}()

	useHTTP2, reason := probeHTTP2(socketPath)
	assert.False(t, useHTTP2)
	assert.Equal(t, reasonTimeout, reason)

	select {
	case conn := <-connCh:
		conn.Close()
	default:
	}
}

func TestProbeHTTP2_DialFailed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-h2probe-dialfail-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "nonexistent.sock")

	useHTTP2, reason := probeHTTP2(socketPath)
	assert.False(t, useHTTP2)
	assert.Equal(t, reasonDialFailed, reason)
}
