package registry

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"nautrouds/internal/core/registry/forwarder"
	"nautrouds/internal/core/registry/loadbalance"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serveUnixOK(t *testing.T, socketPath string) {
	t.Helper()

	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	go server.Serve(l)

	t.Cleanup(func() {
		server.Shutdown(context.Background())
		l.Close()
	})
}

func newServiceDir(t *testing.T, nodeNames ...string) (tmpDir string, nodes []string) {
	t.Helper()
	tmpDir = t.TempDir()
	serviceDir := filepath.Join(tmpDir, "svc")
	require.NoError(t, os.MkdirAll(serviceDir, 0755))

	for _, name := range nodeNames {
		nodePath := filepath.Join(serviceDir, name)
		serveUnixOK(t, nodePath)
		nodes = append(nodes, nodePath)
	}
	return tmpDir, nodes
}

var scanCalls = []struct {
	name  string
	apply func(reg *Registry, baseDir string, nodes []string) error
}{
	{"ServiceScan", func(reg *Registry, baseDir string, nodes []string) error {
		return reg.ApplyServiceScan(baseDir, "svc", nodes)
	}},
	{"FullScan", func(reg *Registry, baseDir string, nodes []string) error {
		discovered := make(map[string]struct{}, len(nodes))
		for _, node := range nodes {
			discovered[node] = struct{}{}
		}
		return reg.ApplyFullScan(baseDir, map[string]map[string]struct{}{"svc": discovered})
	}},
}

func TestScan_NoRebuildWhenNodesUnchanged(t *testing.T) {
	for _, tt := range scanCalls {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, nodes := newServiceDir(t, "node-0.sock")
			reg, err := NewRegistry()
			require.NoError(t, err)
			require.NoError(t, tt.apply(reg, tmpDir, nodes))

			reg.mu.RLock()
			before := reg.services["svc"].roundRobin.Load()
			reg.mu.RUnlock()

			require.NoError(t, tt.apply(reg, tmpDir, nodes))

			reg.mu.RLock()
			after := reg.services["svc"].roundRobin.Load()
			reg.mu.RUnlock()

			assert.Same(t, before, after, "rescan with an unchanged node set must not rebuild the load balancer")
		})
	}
}

func TestScan_WeightedRoundRobinDistribution(t *testing.T) {
	for _, tt := range scanCalls {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, nodes := newServiceDir(t, "node-0@1.sock", "node-1@3.sock")
			reg, err := NewRegistry()
			require.NoError(t, err)
			require.NoError(t, tt.apply(reg, tmpDir, nodes))

			reg.mu.RLock()
			light := reg.nodeMap[nodes[0]].forwarder
			heavy := reg.nodeMap[nodes[1]].forwarder
			reg.mu.RUnlock()

			require.EqualValues(t, 1, light.Weight())
			require.EqualValues(t, 3, heavy.Weight())

			counts := map[*forwarder.Forwarder]int{}
			const rounds = 100
			for range rounds * 4 {
				result := reg.GetForwarders("svc")
				require.Len(t, result, 4)
				counts[result[0]]++
			}

			assert.Equal(t, rounds, counts[light])
			assert.Equal(t, rounds*3, counts[heavy])
		})
	}
}

func TestSetStrategy_RebuildOnlyWhenStrategyChanges(t *testing.T) {
	tmpDir, nodes := newServiceDir(t, "node-0.sock")
	reg, err := NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", nodes))

	reg.setStrategy("svc", loadbalance.StrategyLeastInFlight)

	reg.mu.RLock()
	before := reg.services["svc"].leastInFlight.Load()
	reg.mu.RUnlock()
	require.NotNil(t, before)

	reg.setStrategy("svc", loadbalance.StrategyLeastInFlight)

	reg.mu.RLock()
	after := reg.services["svc"].leastInFlight.Load()
	reg.mu.RUnlock()

	assert.Same(t, before, after, "setStrategy with the same strategy must not rebuild the load balancer")
}
