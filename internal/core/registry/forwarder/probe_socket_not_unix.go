//go:build !unix

package forwarder

import (
	"net"
	"sync/atomic"
	"time"
)

func probeSocket(nodePath string, onFailure chan FailureForwarder, isFailed *atomic.Bool) {
	dialer := net.Dialer{
		Timeout: 20 * time.Millisecond,
	}

	conn, err := dialer.Dial("unix", nodePath)
	if err == nil {
		conn.Close()
		return
	}

	if isFailed.CompareAndSwap(false, true) {
		onFailure <- FailureForwarder{SocketPath: nodePath, Error: err}
	}
}
