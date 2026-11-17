package registry

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"nautrouds/internal/core/registry/forwarder"
	"nautrouds/internal/core/registry/loadbalance"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeForwarder creates a forwarder with a private failure channel so that
// probe failures never reach the registry's listenFailures goroutine.
func makeForwarder(socketPath string) *forwarder.Forwarder {
	failCh := make(chan forwarder.FailureForwarder, 1)
	weight, _ := loadbalance.ParseNodeWeight(socketPath)
	return forwarder.New("svc", socketPath, weight, failCh)
}

func newTestServiceSet(nodes []string, strategy loadbalance.Strategy, nodeMap map[string]*nodeContext) *ServiceSet {
	ss := newServiceSet(nodes, strategy)
	rebuildLoadBalancer(ss, nodeMap)
	return ss
}

func TestGetNodes_NilForMissingService(t *testing.T) {
	reg, err := NewRegistry()
	require.NoError(t, err)

	assert.Nil(t, reg.GetNodes("nonexistent"))
}

func TestGetNodes_ReturnsCopy(t *testing.T) {
	reg, err := NewRegistry()
	require.NoError(t, err)

	reg.mu.Lock()
	reg.services["svc"] = &ServiceSet{nodes: []string{"/fake/node.sock"}}
	reg.mu.Unlock()

	got := reg.GetNodes("svc")
	require.Equal(t, []string{"/fake/node.sock"}, got)

	got[0] = "/changed"
	assert.Equal(t, "/fake/node.sock", reg.GetNodes("svc")[0])
}

func TestGetState_ReturnsSnapshot(t *testing.T) {
	reg, err := NewRegistry()
	require.NoError(t, err)

	reg.mu.Lock()
	reg.services["svcA"] = &ServiceSet{nodes: []string{"/a.sock"}}
	reg.services["svcB"] = &ServiceSet{nodes: []string{"/b1.sock", "/b2.sock"}}
	reg.mu.Unlock()

	state := reg.GetState()
	assert.Len(t, state, 2)
	assert.Equal(t, []string{"/a.sock"}, state["svcA"])
	assert.Len(t, state["svcB"], 2)
}

func TestGetState_EmptyWhenNoServices(t *testing.T) {
	reg, err := NewRegistry()
	require.NoError(t, err)

	assert.Empty(t, reg.GetState())
}

func TestGetForwarders_NilForMissingService(t *testing.T) {
	reg, err := NewRegistry()
	require.NoError(t, err)

	assert.Nil(t, reg.GetForwarders("nonexistent"))
}

func TestGetForwarders_RoundRobin(t *testing.T) {
	tmpDir := t.TempDir()

	reg, err := NewRegistry()
	require.NoError(t, err)

	s1 := filepath.Join(tmpDir, "node1.sock")
	s2 := filepath.Join(tmpDir, "node2.sock")

	f1 := makeForwarder(s1)
	f2 := makeForwarder(s2)

	reg.mu.Lock()
	reg.nodeMap[s1] = &nodeContext{serviceName: "svc", forwarder: f1}
	reg.nodeMap[s2] = &nodeContext{serviceName: "svc", forwarder: f2}
	reg.services["svc"] = newTestServiceSet([]string{s1, s2}, loadbalance.StrategyRoundRobin, reg.nodeMap)
	reg.mu.Unlock()

	got1 := reg.GetForwarders("svc")[0]
	got2 := reg.GetForwarders("svc")[0]

	assert.NotSame(t, got1, got2)

	got3 := reg.GetForwarders("svc")[0]
	assert.Same(t, got1, got3)
}

func TestRetryUnhealthy_SocketUp_NodePromoted(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "node.sock")
	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer ln.Close()

	reg, err := NewRegistry()
	require.NoError(t, err)

	f := makeForwarder(socketPath)
	reg.mu.Lock()
	reg.nodeMap[socketPath] = &nodeContext{serviceName: "svc", forwarder: f}
	reg.unhealthy["svc"] = &ServiceSet{nodes: []string{socketPath}}
	reg.mu.Unlock()

	reg.RetryUnhealthy()

	reg.mu.RLock()
	ss, inServices := reg.services["svc"]
	_, inUnhealthy := reg.unhealthy["svc"]
	reg.mu.RUnlock()

	require.True(t, inServices, "node should be promoted to healthy services")
	assert.Contains(t, ss.nodes, socketPath)
	assert.False(t, inUnhealthy, "node should be removed from unhealthy")
}

func TestRetryUnhealthy_SocketDown_NodeStaysUnhealthy(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "nonexistent.sock")

	reg, err := NewRegistry()
	require.NoError(t, err)

	f := makeForwarder(socketPath)
	reg.mu.Lock()
	reg.nodeMap[socketPath] = &nodeContext{serviceName: "svc", forwarder: f}
	reg.unhealthy["svc"] = &ServiceSet{nodes: []string{socketPath}}
	reg.mu.Unlock()
	os.WriteFile(socketPath, nil, 0555)

	reg.RetryUnhealthy()

	reg.mu.RLock()
	_, inServices := reg.services["svc"]
	us, inUnhealthy := reg.unhealthy["svc"]
	reg.mu.RUnlock()

	assert.False(t, inServices, "node must NOT be promoted when socket is down")
	assert.True(t, inUnhealthy && len(us.nodes) > 0, "node must remain in unhealthy")
}

func TestRetryUnhealthy_NodeMissingFromNodeMap_Cleaned(t *testing.T) {
	tmpDir := t.TempDir()

	reg, err := NewRegistry()
	require.NoError(t, err)

	socketPath := filepath.Join(tmpDir, "gone.sock")

	reg.mu.Lock()
	reg.unhealthy["svc"] = &ServiceSet{nodes: []string{socketPath}}
	reg.mu.Unlock()

	assert.NotPanics(t, func() { reg.RetryUnhealthy() })

	reg.mu.RLock()
	_, inUnhealthy := reg.unhealthy["svc"]
	reg.mu.RUnlock()
	assert.False(t, inUnhealthy, "stale unhealthy entry should be cleaned up")
}
