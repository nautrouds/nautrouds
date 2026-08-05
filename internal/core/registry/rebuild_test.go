package registry

import (
	"os"
	"path/filepath"
	"testing"

	"nautrouds/internal/core/registry/loadbalance"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyServiceScan_NoRebuildWhenNodesUnchanged(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-rebuild-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	serviceDir := filepath.Join(tmpDir, "svc")
	require.NoError(t, os.MkdirAll(serviceDir, 0755))

	nodePath := filepath.Join(serviceDir, "node-0.sock")
	serveUnixOK(t, nodePath)

	reg, err := NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{nodePath}))

	reg.mu.RLock()
	before := reg.services["svc"].roundRobin.Load()
	reg.mu.RUnlock()

	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{nodePath}))

	reg.mu.RLock()
	after := reg.services["svc"].roundRobin.Load()
	reg.mu.RUnlock()

	assert.Same(t, before, after, "rescan with an unchanged node set must not rebuild the load balancer")
}

func TestApplyFullScan_NoRebuildWhenNodesUnchanged(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-rebuild-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	serviceDir := filepath.Join(tmpDir, "svc")
	require.NoError(t, os.MkdirAll(serviceDir, 0755))

	nodePath := filepath.Join(serviceDir, "node-0.sock")
	serveUnixOK(t, nodePath)

	reg, err := NewRegistry()
	require.NoError(t, err)

	byService := map[string]map[string]struct{}{"svc": {nodePath: {}}}
	require.NoError(t, reg.ApplyFullScan(tmpDir, byService))

	reg.mu.RLock()
	before := reg.services["svc"].roundRobin.Load()
	reg.mu.RUnlock()

	require.NoError(t, reg.ApplyFullScan(tmpDir, byService))

	reg.mu.RLock()
	after := reg.services["svc"].roundRobin.Load()
	reg.mu.RUnlock()

	assert.Same(t, before, after, "a full scan with an unchanged node set must not rebuild the load balancer")
}

func TestSetStrategy_NoRebuildWhenStrategyUnchanged(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-rebuild-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	serviceDir := filepath.Join(tmpDir, "svc")
	require.NoError(t, os.MkdirAll(serviceDir, 0755))

	nodePath := filepath.Join(serviceDir, "node-0.sock")
	serveUnixOK(t, nodePath)

	reg, err := NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{nodePath}))

	reg.setStrategy("svc", loadbalance.StrategyLeastInFlight)

	reg.mu.RLock()
	before := reg.services["svc"].leastInFlight.Load()
	reg.mu.RUnlock()

	reg.setStrategy("svc", loadbalance.StrategyLeastInFlight)

	reg.mu.RLock()
	after := reg.services["svc"].leastInFlight.Load()
	reg.mu.RUnlock()

	assert.Same(t, before, after, "setStrategy with the same strategy must not rebuild the load balancer")
}

func TestSetStrategy_RebuildsWhenStrategyChanges(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-rebuild-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	serviceDir := filepath.Join(tmpDir, "svc")
	require.NoError(t, os.MkdirAll(serviceDir, 0755))

	nodePath := filepath.Join(serviceDir, "node-0.sock")
	serveUnixOK(t, nodePath)

	reg, err := NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{nodePath}))

	reg.setStrategy("svc", loadbalance.StrategyLeastInFlight)

	reg.mu.RLock()
	assert.NotNil(t, reg.services["svc"].leastInFlight.Load())
	reg.mu.RUnlock()
}
