package registry

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"nautrouds/internal/core/registry/forwarder"
	"nautrouds/internal/core/registry/loadbalance"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockingNode holds n concurrent requests in flight against fwd until unblock() is called.
type blockingNode struct {
	fwd     *forwarder.Forwarder
	path    string
	release chan struct{}
	wg      sync.WaitGroup
	cleanup func()
}

func newBlockingNode(t *testing.T, n int) *blockingNode {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "nautrouds-loadbalance-*")
	require.NoError(t, err)

	socketPath := filepath.Join(tmpDir, "node.sock")
	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	started := make(chan struct{}, n)
	release := make(chan struct{})
	server := &http.Server{
		// forwarder.New()'s async HTTP/2 probe hits this socket too; its preface parses as method PRI, so ignore non-GET.
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusOK)
				return
			}
			started <- struct{}{}
			<-release
			w.WriteHeader(http.StatusOK)
		}),
	}
	go server.Serve(l)

	node := &blockingNode{
		fwd:     makeForwarder(socketPath),
		path:    socketPath,
		release: release,
		cleanup: func() {
			server.Shutdown(context.Background())
			l.Close()
			os.RemoveAll(tmpDir)
		},
	}

	for range n {
		node.wg.Go(func() {
			req := httptest.NewRequest("GET", "http://example.com/", nil)
			w := httptest.NewRecorder()
			node.fwd.Forward(w, req)
		})
	}

	for range n {
		select {
		case <-started:
		case <-time.After(1 * time.Second):
			t.Fatal("timed out waiting for a request to start")
		}
	}

	return node
}

func (n *blockingNode) unblock() {
	close(n.release)
	n.wg.Wait()
	n.cleanup()
}

func TestSortByLoad_RealForwarders(t *testing.T) {
	idle := makeForwarder(filepath.Join(t.TempDir(), "idle.sock"))

	loaded1 := newBlockingNode(t, 1)
	defer loaded1.unblock()

	loaded2 := newBlockingNode(t, 2)
	defer loaded2.unblock()

	require.EqualValues(t, 0, idle.InFlightWeight())
	require.EqualValues(t, 1, loaded1.fwd.InFlightWeight())
	require.EqualValues(t, 2, loaded2.fwd.InFlightWeight())

	result := loadbalance.SortByLoad([]*forwarder.Forwarder{loaded2.fwd, idle, loaded1.fwd})

	assert.Same(t, idle, result[0])
	assert.Same(t, loaded1.fwd, result[1])
	assert.Same(t, loaded2.fwd, result[2])
}

func TestGetForwarders_LeastInFlightStrategy(t *testing.T) {
	reg, err := NewRegistry()
	require.NoError(t, err)

	idlePath := filepath.Join(t.TempDir(), "idle.sock")
	idle := makeForwarder(idlePath)

	loaded1 := newBlockingNode(t, 1)
	defer loaded1.unblock()

	loaded2 := newBlockingNode(t, 2)
	defer loaded2.unblock()

	reg.mu.Lock()
	reg.nodeMap[idlePath] = &nodeContext{serviceName: "svc", forwarder: idle}
	reg.nodeMap[loaded1.path] = &nodeContext{serviceName: "svc", forwarder: loaded1.fwd}
	reg.nodeMap[loaded2.path] = &nodeContext{serviceName: "svc", forwarder: loaded2.fwd}
	reg.services["svc"] = newTestServiceSet([]string{loaded2.path, idlePath, loaded1.path}, loadbalance.StrategyLeastInFlight, reg.nodeMap)
	reg.mu.Unlock()

	result := reg.GetForwarders("svc")
	require.Len(t, result, 3)
	assert.Same(t, idle, result[0])
	assert.Same(t, loaded1.fwd, result[1])
	assert.Same(t, loaded2.fwd, result[2])
}

func TestGetForwarders_P2CStrategy(t *testing.T) {
	reg, err := NewRegistry()
	require.NoError(t, err)

	idlePath := filepath.Join(t.TempDir(), "idle.sock")
	idle := makeForwarder(idlePath)

	loaded := newBlockingNode(t, 1)
	defer loaded.unblock()

	reg.mu.Lock()
	reg.nodeMap[idlePath] = &nodeContext{serviceName: "svc", forwarder: idle}
	reg.nodeMap[loaded.path] = &nodeContext{serviceName: "svc", forwarder: loaded.fwd}
	reg.services["svc"] = newTestServiceSet([]string{loaded.path, idlePath}, loadbalance.StrategyP2C, reg.nodeMap)
	reg.mu.Unlock()

	for range 20 {
		result := reg.GetForwarders("svc")
		require.Len(t, result, 2)
		assert.Same(t, idle, result[0])
		assert.Same(t, loaded.fwd, result[1])
	}
}

// markerPath returns the path of the "<token>.strategy" marker file under tmpDir/svc.
func markerPath(tmpDir, token string) string {
	return filepath.Join(tmpDir, "svc", token+strategyFileSuffix)
}

// setUpSvc creates tmpDir/svc, an empty "<token>.strategy" marker for each token
// given, and an alive node socket, returning the pieces callers need to drive scans.
func setUpSvc(t *testing.T, tokens ...string) (tmpDir, socketPath string, node *blockingNode) {
	t.Helper()
	tmpDir = t.TempDir()
	svcDir := filepath.Join(tmpDir, "svc")
	require.NoError(t, os.MkdirAll(svcDir, 0755))

	for _, token := range tokens {
		require.NoError(t, os.WriteFile(markerPath(tmpDir, token), nil, 0644))
	}

	// Needs a live listener: a dangling socket path triggers async ECONNREFUSED
	// removal (see orphan_integration_test.go), which would race the assertions below.
	node = newBlockingNode(t, 0)
	socketPath = node.path
	return
}

func TestReadStrategy(t *testing.T) {
	t.Run("NoMarker", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "svc"), 0755))
		assert.Equal(t, loadbalance.StrategyRoundRobin, readStrategyAtCreation(tmpDir, "svc"))
	})

	t.Run("RoundRobinMarker", func(t *testing.T) {
		tmpDir, _, node := setUpSvc(t, "round_robin")
		defer node.unblock()
		assert.Equal(t, loadbalance.StrategyRoundRobin, readStrategyAtCreation(tmpDir, "svc"))
	})

	t.Run("LeastInFlightMarker", func(t *testing.T) {
		tmpDir, _, node := setUpSvc(t, "least_in_flight")
		defer node.unblock()
		assert.Equal(t, loadbalance.StrategyLeastInFlight, readStrategyAtCreation(tmpDir, "svc"))
	})

	t.Run("P2CMarker", func(t *testing.T) {
		tmpDir, _, node := setUpSvc(t, "p2c")
		defer node.unblock()
		assert.Equal(t, loadbalance.StrategyP2C, readStrategyAtCreation(tmpDir, "svc"))
	})

	t.Run("BothMarkersPriority", func(t *testing.T) {
		tmpDir, _, node := setUpSvc(t, "round_robin", "least_in_flight")
		defer node.unblock()
		assert.Equal(t, loadbalance.StrategyLeastInFlight, readStrategyAtCreation(tmpDir, "svc"))
	})

	t.Run("P2CPriorityOverLeastInFlight", func(t *testing.T) {
		tmpDir, _, node := setUpSvc(t, "least_in_flight", "p2c")
		defer node.unblock()
		assert.Equal(t, loadbalance.StrategyP2C, readStrategyAtCreation(tmpDir, "svc"))
	})

	t.Run("StatErrorNotNotExist", func(t *testing.T) {
		tmpDir := t.TempDir()
		// "svc" is a file, not a directory, so stat-ing a candidate under it fails with ENOTDIR, not not-exist.
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "svc"), nil, 0644))
		assert.Equal(t, loadbalance.StrategyRoundRobin, readStrategyAtCreation(tmpDir, "svc"))
	})
}

func TestResolveMarkerStrategy(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		assert.Equal(t, loadbalance.StrategyRoundRobin, resolveMarkerStrategy(nil, "svc"))
	})

	t.Run("UnknownToken", func(t *testing.T) {
		assert.Equal(t, loadbalance.StrategyRoundRobin, resolveMarkerStrategy([]string{"/svc/bogus.strategy"}, "svc"))
	})

	t.Run("PriorityWhenBothPresent", func(t *testing.T) {
		paths := []string{"/svc/round_robin.strategy", "/svc/least_in_flight.strategy"}
		assert.Equal(t, loadbalance.StrategyLeastInFlight, resolveMarkerStrategy(paths, "svc"))
	})

	t.Run("P2CToken", func(t *testing.T) {
		assert.Equal(t, loadbalance.StrategyP2C, resolveMarkerStrategy([]string{"/svc/p2c.strategy"}, "svc"))
	})

	t.Run("P2CPriorityOverLeastInFlight", func(t *testing.T) {
		paths := []string{"/svc/least_in_flight.strategy", "/svc/p2c.strategy"}
		assert.Equal(t, loadbalance.StrategyP2C, resolveMarkerStrategy(paths, "svc"))
	})
}

func TestApplyServiceScan_ReadsStrategyOnlyAtCreation(t *testing.T) {
	tmpDir, socketPath, node := setUpSvc(t, "least_in_flight")
	defer node.unblock()

	reg, err := NewRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{socketPath}))
	reg.mu.RLock()
	assert.Equal(t, loadbalance.StrategyLeastInFlight, reg.services["svc"].strategy)
	reg.mu.RUnlock()

	// The .sock handler only reads markers when creating a ServiceSet; a rescan of
	// an already-existing service must not re-check them, even though they changed.
	require.NoError(t, os.Remove(markerPath(tmpDir, "least_in_flight")))
	require.NoError(t, os.WriteFile(markerPath(tmpDir, "round_robin"), nil, 0644))
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{socketPath}))

	reg.mu.RLock()
	assert.Equal(t, loadbalance.StrategyLeastInFlight, reg.services["svc"].strategy)
	reg.mu.RUnlock()
}

func TestStrategyHandler_AppliesUnconditionally(t *testing.T) {
	tmpDir, socketPath, node := setUpSvc(t, "least_in_flight")
	defer node.unblock()

	reg, err := NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{socketPath}))

	sh := NewStrategyHandler(reg)
	leastInFlightMarker := markerPath(tmpDir, "least_in_flight")
	require.NoError(t, sh.ApplyServiceScan(tmpDir, "svc", []string{leastInFlightMarker}))
	reg.mu.RLock()
	assert.Equal(t, loadbalance.StrategyLeastInFlight, reg.services["svc"].strategy)
	reg.mu.RUnlock()

	// Rescanning the exact same, unchanged marker must still apply it: proves there's
	// no change-detection cache being skipped, unlike the old mtime-based design.
	reg.mu.Lock()
	reg.services["svc"].strategy = loadbalance.StrategyRoundRobin
	reg.mu.Unlock()

	require.NoError(t, sh.ApplyServiceScan(tmpDir, "svc", []string{leastInFlightMarker}))
	reg.mu.RLock()
	assert.Equal(t, loadbalance.StrategyLeastInFlight, reg.services["svc"].strategy)
	reg.mu.RUnlock()
}

func TestStrategyHandler_FileRemovedResetsToDefault(t *testing.T) {
	tmpDir, socketPath, node := setUpSvc(t, "least_in_flight")
	defer node.unblock()

	reg, err := NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{socketPath}))

	sh := NewStrategyHandler(reg)
	require.NoError(t, sh.ApplyServiceScan(tmpDir, "svc", []string{markerPath(tmpDir, "least_in_flight")}))
	reg.mu.RLock()
	assert.Equal(t, loadbalance.StrategyLeastInFlight, reg.services["svc"].strategy)
	reg.mu.RUnlock()

	require.NoError(t, sh.ApplyServiceScan(tmpDir, "svc", nil))
	reg.mu.RLock()
	assert.Equal(t, loadbalance.StrategyRoundRobin, reg.services["svc"].strategy)
	reg.mu.RUnlock()
}

func TestStrategyHandler_ApplyFullScan_ResetsServiceMissingFromByService(t *testing.T) {
	tmpDir, socketPath, node := setUpSvc(t, "least_in_flight")
	defer node.unblock()

	reg, err := NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{socketPath}))

	sh := NewStrategyHandler(reg)
	require.NoError(t, sh.ApplyServiceScan(tmpDir, "svc", []string{markerPath(tmpDir, "least_in_flight")}))
	reg.mu.RLock()
	assert.Equal(t, loadbalance.StrategyLeastInFlight, reg.services["svc"].strategy)
	reg.mu.RUnlock()

	require.NoError(t, sh.ApplyFullScan(tmpDir, map[string]map[string]struct{}{}))
	reg.mu.RLock()
	assert.Equal(t, loadbalance.StrategyRoundRobin, reg.services["svc"].strategy)
	reg.mu.RUnlock()
}

func TestStrategyHandler_ApplyFullScan_AppliesMarkerForExistingService(t *testing.T) {
	tmpDir, socketPath, node := setUpSvc(t)
	defer node.unblock()

	reg, err := NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{socketPath}))
	reg.mu.RLock()
	assert.Equal(t, loadbalance.StrategyRoundRobin, reg.services["svc"].strategy)
	reg.mu.RUnlock()

	leastInFlightMarker := markerPath(tmpDir, "least_in_flight")
	require.NoError(t, os.WriteFile(leastInFlightMarker, nil, 0644))

	sh := NewStrategyHandler(reg)
	byService := map[string]map[string]struct{}{
		"svc": {leastInFlightMarker: struct{}{}},
	}
	require.NoError(t, sh.ApplyFullScan(tmpDir, byService))

	reg.mu.RLock()
	assert.Equal(t, loadbalance.StrategyLeastInFlight, reg.services["svc"].strategy)
	reg.mu.RUnlock()
}

func TestGetForwarders_RoundRobinStrategyUnchanged(t *testing.T) {
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

	first := reg.GetForwarders("svc")
	second := reg.GetForwarders("svc")
	require.Len(t, first, 2)
	require.Len(t, second, 2)
	assert.NotSame(t, first[0], second[0])

	third := reg.GetForwarders("svc")
	assert.Same(t, first[0], third[0])
}
