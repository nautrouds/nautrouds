//go:build unix

package registry

import (
	"nautrouds/internal/core/registry/forwarder"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createOrphanSocket uses syscall directly so the file survives Close;
// net.Listen sets unlink:true and would remove it on Close.
func createOrphanSocket(t *testing.T, path string) {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	require.NoError(t, err)
	require.NoError(t, syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}))
	require.NoError(t, syscall.Listen(fd, 1))
	syscall.Close(fd)
}

func TestOrphanSocket_ECONNREFUSED_NodeRemovedAndFileDeleted(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-orphan-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	serviceDir := filepath.Join(tmpDir, "svc")
	require.NoError(t, os.MkdirAll(serviceDir, 0755))
	socketPath := filepath.Join(serviceDir, "node.sock")

	createOrphanSocket(t, socketPath)

	_, err = os.Stat(socketPath)
	require.NoError(t, err, "socket file must exist before scan")

	reg, err := NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{socketPath}))

	require.Eventually(t, func() bool {
		reg.mu.RLock()
		_, exists := reg.nodeMap[socketPath]
		reg.mu.RUnlock()
		return !exists
	}, 2*time.Second, 20*time.Millisecond,
		"node was not removed; ECONNREFUSED may not have been received")

	assert.Nil(t, reg.GetNodes("svc"))

	require.Eventually(t, func() bool {
		_, statErr := os.Stat(socketPath)
		return os.IsNotExist(statErr)
	}, 500*time.Millisecond, 10*time.Millisecond,
		"orphan socket file was not deleted")
}

func TestOrphanSocket_NonECONNREFUSED_NodeMovedToUnhealthy(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-noent-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "svc", "node.sock")

	reg, err := NewRegistry()
	require.NoError(t, err)

	reg.mu.Lock()
	reg.nodeMap[socketPath] = &nodeContext{serviceName: "svc"}
	reg.services["svc"] = &ServiceSet{nodes: []string{socketPath}}
	reg.mu.Unlock()

	reg.failureChan <- forwarder.FailureForwarder{SocketPath: socketPath, Error: syscall.ENOENT}

	require.Eventually(t, func() bool {
		reg.mu.RLock()
		defer reg.mu.RUnlock()
		us, ok := reg.unhealthy["svc"]
		if !ok {
			return false
		}
		for _, n := range us.nodes {
			if n == socketPath {
				return true
			}
		}
		return false
	}, 2*time.Second, 20*time.Millisecond,
		"node should be in unhealthy for non-ECONNREFUSED errors")

	reg.mu.RLock()
	_, inNodeMap := reg.nodeMap[socketPath]
	reg.mu.RUnlock()
	assert.True(t, inNodeMap, "node must remain in nodeMap for non-ECONNREFUSED errors")
}
