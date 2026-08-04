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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLoad struct {
	name string
	load int64
}

func (f *fakeLoad) InFlightWeight() int64 { return f.load }

func loadsOf(items []*fakeLoad) []int64 {
	loads := make([]int64, len(items))
	for i, it := range items {
		loads[i] = it.InFlightWeight()
	}
	return loads
}

func TestSortByLoad(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		assert.Empty(t, SortByLoad[*fakeLoad](nil))
		assert.Empty(t, SortByLoad([]*fakeLoad{}))
	})

	t.Run("Single", func(t *testing.T) {
		f := &fakeLoad{name: "a", load: 3}
		result := SortByLoad([]*fakeLoad{f})
		require.Len(t, result, 1)
		assert.Same(t, f, result[0])
	})

	t.Run("AlreadySorted", func(t *testing.T) {
		in := []*fakeLoad{{name: "a", load: 0}, {name: "b", load: 1}, {name: "c", load: 2}}
		result := SortByLoad(in)
		assert.Equal(t, []int64{0, 1, 2}, loadsOf(result))
		for i := range in {
			assert.Same(t, in[i], result[i])
		}
	})

	t.Run("ReverseSorted", func(t *testing.T) {
		a, b, c := &fakeLoad{name: "a", load: 5}, &fakeLoad{name: "b", load: 3}, &fakeLoad{name: "c", load: 1}
		result := SortByLoad([]*fakeLoad{a, b, c})
		assert.Equal(t, []int64{1, 3, 5}, loadsOf(result))
		assert.Same(t, c, result[0])
		assert.Same(t, b, result[1])
		assert.Same(t, a, result[2])
	})

	t.Run("Ties", func(t *testing.T) {
		a := &fakeLoad{name: "a", load: 1}
		b := &fakeLoad{name: "b", load: 2}
		c := &fakeLoad{name: "c", load: 1}
		d := &fakeLoad{name: "d", load: 2}
		result := SortByLoad([]*fakeLoad{a, b, c, d})
		assert.Equal(t, []int64{1, 1, 2, 2}, loadsOf(result))
		// stable sort: ties keep original order
		assert.Same(t, a, result[0])
		assert.Same(t, c, result[1])
		assert.Same(t, b, result[2])
		assert.Same(t, d, result[3])
	})

	t.Run("Mixed", func(t *testing.T) {
		a := &fakeLoad{name: "a", load: 4}
		b := &fakeLoad{name: "b", load: 1}
		c := &fakeLoad{name: "c", load: 4}
		d := &fakeLoad{name: "d", load: 2}
		e := &fakeLoad{name: "e", load: 1}
		result := SortByLoad([]*fakeLoad{a, b, c, d, e})
		assert.Equal(t, []int64{1, 1, 2, 4, 4}, loadsOf(result))
		assert.Same(t, b, result[0])
		assert.Same(t, e, result[1])
		assert.Same(t, d, result[2])
		assert.Same(t, a, result[3])
		assert.Same(t, c, result[4])
	})

	t.Run("InputNotMutated", func(t *testing.T) {
		a, b, c := &fakeLoad{name: "a", load: 5}, &fakeLoad{name: "b", load: 3}, &fakeLoad{name: "c", load: 1}
		in := []*fakeLoad{a, b, c}
		SortByLoad(in)
		assert.Same(t, a, in[0])
		assert.Same(t, b, in[1])
		assert.Same(t, c, in[2])
	})
}

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

	result := SortByLoad([]*forwarder.Forwarder{loaded2.fwd, idle, loaded1.fwd})

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
	reg.services["svc"] = &ServiceSet{
		nodes:    []string{loaded2.path, idlePath, loaded1.path},
		strategy: StrategyLeastInFlight,
	}
	reg.mu.Unlock()

	result := reg.GetForwarders("svc")
	require.Len(t, result, 3)
	assert.Same(t, idle, result[0])
	assert.Same(t, loaded1.fwd, result[1])
	assert.Same(t, loaded2.fwd, result[2])
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
		assert.Equal(t, StrategyRoundRobin, readStrategyAtCreation(tmpDir, "svc"))
	})

	t.Run("RoundRobinMarker", func(t *testing.T) {
		tmpDir, _, node := setUpSvc(t, "round_robin")
		defer node.unblock()
		assert.Equal(t, StrategyRoundRobin, readStrategyAtCreation(tmpDir, "svc"))
	})

	t.Run("LeastInFlightMarker", func(t *testing.T) {
		tmpDir, _, node := setUpSvc(t, "least_in_flight")
		defer node.unblock()
		assert.Equal(t, StrategyLeastInFlight, readStrategyAtCreation(tmpDir, "svc"))
	})

	t.Run("BothMarkersPriority", func(t *testing.T) {
		tmpDir, _, node := setUpSvc(t, "round_robin", "least_in_flight")
		defer node.unblock()
		assert.Equal(t, StrategyLeastInFlight, readStrategyAtCreation(tmpDir, "svc"))
	})

	t.Run("StatErrorNotNotExist", func(t *testing.T) {
		tmpDir := t.TempDir()
		// "svc" is a file, not a directory, so stat-ing a candidate under it fails with ENOTDIR, not not-exist.
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "svc"), nil, 0644))
		assert.Equal(t, StrategyRoundRobin, readStrategyAtCreation(tmpDir, "svc"))
	})
}

func TestResolveMarkerStrategy(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		assert.Equal(t, StrategyRoundRobin, resolveMarkerStrategy(nil, "svc"))
	})

	t.Run("UnknownToken", func(t *testing.T) {
		assert.Equal(t, StrategyRoundRobin, resolveMarkerStrategy([]string{"/svc/bogus.strategy"}, "svc"))
	})

	t.Run("PriorityWhenBothPresent", func(t *testing.T) {
		paths := []string{"/svc/round_robin.strategy", "/svc/least_in_flight.strategy"}
		assert.Equal(t, StrategyLeastInFlight, resolveMarkerStrategy(paths, "svc"))
	})
}

func TestApplyServiceScan_ReadsStrategyOnlyAtCreation(t *testing.T) {
	tmpDir, socketPath, node := setUpSvc(t, "least_in_flight")
	defer node.unblock()

	reg, err := NewRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{socketPath}))
	reg.mu.RLock()
	assert.Equal(t, StrategyLeastInFlight, reg.services["svc"].strategy)
	reg.mu.RUnlock()

	// The .sock handler only reads markers when creating a ServiceSet; a rescan of
	// an already-existing service must not re-check them, even though they changed.
	require.NoError(t, os.Remove(markerPath(tmpDir, "least_in_flight")))
	require.NoError(t, os.WriteFile(markerPath(tmpDir, "round_robin"), nil, 0644))
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{socketPath}))

	reg.mu.RLock()
	assert.Equal(t, StrategyLeastInFlight, reg.services["svc"].strategy)
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
	assert.Equal(t, StrategyLeastInFlight, reg.services["svc"].strategy)
	reg.mu.RUnlock()

	// Rescanning the exact same, unchanged marker must still apply it: proves there's
	// no change-detection cache being skipped, unlike the old mtime-based design.
	reg.mu.Lock()
	reg.services["svc"].strategy = StrategyRoundRobin
	reg.mu.Unlock()

	require.NoError(t, sh.ApplyServiceScan(tmpDir, "svc", []string{leastInFlightMarker}))
	reg.mu.RLock()
	assert.Equal(t, StrategyLeastInFlight, reg.services["svc"].strategy)
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
	assert.Equal(t, StrategyLeastInFlight, reg.services["svc"].strategy)
	reg.mu.RUnlock()

	require.NoError(t, sh.ApplyServiceScan(tmpDir, "svc", nil))
	reg.mu.RLock()
	assert.Equal(t, StrategyRoundRobin, reg.services["svc"].strategy)
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
	assert.Equal(t, StrategyLeastInFlight, reg.services["svc"].strategy)
	reg.mu.RUnlock()

	require.NoError(t, sh.ApplyFullScan(tmpDir, map[string]map[string]struct{}{}))
	reg.mu.RLock()
	assert.Equal(t, StrategyRoundRobin, reg.services["svc"].strategy)
	reg.mu.RUnlock()
}

func TestStrategyHandler_ApplyFullScan_AppliesMarkerForExistingService(t *testing.T) {
	tmpDir, socketPath, node := setUpSvc(t)
	defer node.unblock()

	reg, err := NewRegistry()
	require.NoError(t, err)
	require.NoError(t, reg.ApplyServiceScan(tmpDir, "svc", []string{socketPath}))
	reg.mu.RLock()
	assert.Equal(t, StrategyRoundRobin, reg.services["svc"].strategy)
	reg.mu.RUnlock()

	leastInFlightMarker := markerPath(tmpDir, "least_in_flight")
	require.NoError(t, os.WriteFile(leastInFlightMarker, nil, 0644))

	sh := NewStrategyHandler(reg)
	byService := map[string]map[string]struct{}{
		"svc": {leastInFlightMarker: struct{}{}},
	}
	require.NoError(t, sh.ApplyFullScan(tmpDir, byService))

	reg.mu.RLock()
	assert.Equal(t, StrategyLeastInFlight, reg.services["svc"].strategy)
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
	reg.services["svc"] = &ServiceSet{nodes: []string{s1, s2}} // zero-value strategy: StrategyRoundRobin
	reg.mu.Unlock()

	first := reg.GetForwarders("svc")
	second := reg.GetForwarders("svc")
	require.Len(t, first, 2)
	require.Len(t, second, 2)
	assert.NotSame(t, first[0], second[0])

	third := reg.GetForwarders("svc")
	assert.Same(t, first[0], third[0])
}
