//go:build unix

package forwarder

import (
	"sync/atomic"
	"syscall"
)

func probeSocket(nodePath string, onFailure chan FailureForwarder, isFailed *atomic.Bool) {
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return
	}
	defer syscall.Close(fd)

	addr := &syscall.SockaddrUnix{Name: nodePath}
	err = syscall.Connect(fd, addr)

	if err != nil {
		if isFailed.CompareAndSwap(false, true) {
			onFailure <- FailureForwarder{SocketPath: nodePath, Error: err}
		}
	}
}
