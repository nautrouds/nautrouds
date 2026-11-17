package forwarder

import (
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serveHTTP(h2 bool) func(l net.Listener) {
	return func(l net.Listener) {
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}
		if h2 {
			server.Protocols = new(http.Protocols)
			server.Protocols.SetHTTP1(true)
			server.Protocols.SetUnencryptedHTTP2(true)
		}
		server.Serve(l)
	}
}

func TestProbeHTTP2(t *testing.T) {
	goAwayHTTP11Required := []byte{
		0x00, 0x00, 0x08,
		0x07,
		0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x0d,
	}

	tests := []struct {
		name       string
		serve      func(l net.Listener)
		wantH2     bool
		wantReason string
	}{
		{"Supported", serveHTTP(true), true, reasonSupported},
		{"Unsupported", serveHTTP(false), false, reasonUnsupported},
		{"UnsupportedRawReset", func(l net.Listener) {
			if conn, err := l.Accept(); err == nil {
				conn.Close()
			}
		}, false, reasonUnsupported},
		{"Declined", func(l net.Listener) {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			conn.Read(make([]byte, 64))
			conn.Write(goAwayHTTP11Required)
		}, false, reasonDeclined},
		{"Timeout", func(l net.Listener) {
			if conn, err := l.Accept(); err == nil {
				defer conn.Close()
				conn.Read(make([]byte, 1024))
				conn.Read(make([]byte, 1024))
			}
		}, false, reasonTimeout},
		{"DialFailed", nil, false, reasonDialFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			socketPath := filepath.Join(t.TempDir(), "probe.sock")
			if tt.serve != nil {
				l, err := net.Listen("unix", socketPath)
				require.NoError(t, err)
				defer l.Close()
				go tt.serve(l)
			}

			useHTTP2, reason := probeHTTP2(socketPath)
			assert.Equal(t, tt.wantH2, useHTTP2)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}
