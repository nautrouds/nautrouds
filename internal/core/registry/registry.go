package registry

import (
	"errors"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/metrics"
	"nautrouds/internal/core/registry/forwarder"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"
)

type Registry struct {
	mu      sync.RWMutex
	baseDir string

	services  map[string]*ServiceSet // serviceName -> node list & load balanced index
	unhealthy map[string]*ServiceSet
	nodeMap   map[string]*nodeContext // nodePath -> forwarder & service name

	failureChan chan forwarder.FailureForwarder
}

type ServiceSet struct {
	nodes     []string
	nodeIndex map[string]int // node -> position in nodes; O(1) lookup & removal
	index     atomic.Uint32
}

func newServiceSet(nodes []string) *ServiceSet {
	ss := &ServiceSet{}
	ss.replace(nodes)
	return ss
}

func (ss *ServiceSet) ensureNodeIndex() {
	if ss.nodeIndex != nil {
		return
	}
	ss.nodeIndex = make(map[string]int, len(ss.nodes))
	for i, n := range ss.nodes {
		ss.nodeIndex[n] = i
	}
}

func (ss *ServiceSet) replace(nodes []string) {
	ss.nodes = nodes
	ss.nodeIndex = make(map[string]int, len(nodes))
	for i, n := range nodes {
		ss.nodeIndex[n] = i
	}
}

func (ss *ServiceSet) contains(node string) bool {
	ss.ensureNodeIndex()
	_, ok := ss.nodeIndex[node]
	return ok
}

func (ss *ServiceSet) add(node string) bool {
	ss.ensureNodeIndex()
	if _, ok := ss.nodeIndex[node]; ok {
		return false
	}
	ss.nodeIndex[node] = len(ss.nodes)
	ss.nodes = append(ss.nodes, node)
	return true
}

func (ss *ServiceSet) remove(node string) bool {
	ss.ensureNodeIndex()
	removedIdx, ok := ss.nodeIndex[node]
	if !ok {
		return false
	}
	lastIdx := len(ss.nodes) - 1
	lastNode := ss.nodes[lastIdx]
	ss.nodes[removedIdx] = lastNode
	ss.nodes = ss.nodes[:lastIdx]
	if lastNode != node {
		ss.nodeIndex[lastNode] = removedIdx
	}
	delete(ss.nodeIndex, node)
	return true
}

type nodeContext struct {
	serviceName string
	forwarder   *forwarder.Forwarder
}

func NewRegistry(baseDir string) (*Registry, error) {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}

	r := &Registry{
		services:    make(map[string]*ServiceSet),
		unhealthy:   make(map[string]*ServiceSet),
		nodeMap:     make(map[string]*nodeContext),
		baseDir:     strings.TrimRight(filepath.ToSlash(absBase), "/"),
		failureChan: make(chan forwarder.FailureForwarder, 100),
	}

	go r.listenFailures()

	return r, nil
}

func (r *Registry) BaseDir() string {
	return r.baseDir
}

func (r *Registry) listenFailures() {
	for failure := range r.failureChan {
		removeNeeded := errors.Is(failure.Error, syscall.ECONNREFUSED)

		logs.Out.Error("Node failure detected", zap.String("socketPath", failure.SocketPath), zap.Error(failure.Error))

		r.mu.Lock()
		ctx, ok := r.nodeMap[failure.SocketPath]
		if ok {
			metrics.Global.NodeFailuresTotal.WithLabelValues(ctx.serviceName, failure.SocketPath).Inc()
			if removeNeeded {
				r.removeNodeUnsafe(failure.SocketPath, true)
			} else {
				r.moveToUnhealthyUnsafe(ctx.serviceName, failure.SocketPath)
			}
		}
		r.mu.Unlock()

		if removeNeeded {
			r.RemoveNode(failure.SocketPath, true)
		}

	}
}

func (r *Registry) moveToUnhealthyUnsafe(serviceName, nodePath string) {
	if ss, ok := r.services[serviceName]; ok {
		ss.remove(nodePath)
		if len(ss.nodes) == 0 {
			delete(r.services, serviceName)
		}
		metrics.Global.ServiceNodesActive.WithLabelValues(serviceName).Set(float64(len(ss.nodes)))
	}

	us, ok := r.unhealthy[serviceName]
	if !ok {
		us = newServiceSet(nil)
		r.unhealthy[serviceName] = us
	}
	us.add(nodePath)
}

func (r *Registry) NodeCount(serviceName string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ss, ok := r.services[serviceName]
	if !ok {
		return 0
	}
	return len(ss.nodes)
}

// GetNodes returns a copy of the current physical nodes for a service
func (r *Registry) GetNodes(serviceName string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ss, ok := r.services[serviceName]
	if !ok {
		return nil
	}
	return slices.Clone(ss.nodes)
}

// GetState returns a snapshot of all services and their current nodes.
func (r *Registry) GetState() map[string][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state := make(map[string][]string)
	for name, ss := range r.services {
		state[name] = slices.Clone(ss.nodes)
	}
	return state
}

func (r *Registry) GetForwarder(serviceName string) (*forwarder.Forwarder, error) {
	r.mu.RLock()
	ss, exists := r.services[serviceName]
	r.mu.RUnlock()

	if !exists || len(ss.nodes) == 0 {
		return nil, os.ErrNotExist
	}

	idx := ss.index.Add(1) % uint32(len(ss.nodes))
	nodePath := ss.nodes[idx]

	r.mu.RLock()
	ctx, ok := r.nodeMap[nodePath]
	r.mu.RUnlock()

	if !ok {
		return nil, os.ErrNotExist
	}

	return ctx.forwarder, nil
}

// GetForwarders returns all healthy forwarders for a service, ordered starting
// from the round-robin position. The index advances only once per call so that
// retry loops within a single request visit each node exactly once.
func (r *Registry) GetForwarders(serviceName string) []*forwarder.Forwarder {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ss, exists := r.services[serviceName]
	if !exists || len(ss.nodes) == 0 {
		return nil
	}

	n := len(ss.nodes)
	start := int(ss.index.Add(1)) % n
	result := make([]*forwarder.Forwarder, 0, n)
	for i := range n {
		ctx, ok := r.nodeMap[ss.nodes[(start+i)%n]]
		if ok {
			result = append(result, ctx.forwarder)
		}
	}
	return result
}

// Scan satisfies the Watcher's expectation. It performs either a full scan
// or a targeted scan based on the provided target string.
func (r *Registry) Scan(target string) error {
	start := time.Now()
	defer func() {
		metrics.Global.RegistryScanDuration.Observe(time.Since(start).Seconds())
	}()

	// If target is empty or matches baseDir, perform full scan
	if target == "" || target == r.baseDir {
		return r.fullScan()
	}

	return r.scanService(target)
}

func (r *Registry) fullScan() error {
	scannedState := make(map[string][]string)
	scannedPaths := make(map[string]string)
	baseLen := len(r.baseDir) + 1

	err := filepath.WalkDir(r.baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.HasSuffix(d.Name(), ".sock") {
			rel := filepath.ToSlash(path[baseLen:])
			if !strings.Contains(rel, "/") {
				return nil
			}

			serviceName := filepath.Dir(rel)
			scannedState[serviceName] = append(scannedState[serviceName], path)
			scannedPaths[path] = serviceName
		}
		return nil
	})

	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Remove services/nodes no longer present
	for path, ctx := range r.nodeMap {
		if svcName, found := scannedPaths[path]; !found || svcName != ctx.serviceName {
			r.removeNodeUnsafe(path, false)
			r.removeFromUnhealthyUnsafe(ctx.serviceName, path)
		}
	}

	// 2. Add new nodes and update services
	for svcName, nodes := range scannedState {
		healthyNodes := make([]string, 0, len(nodes))
		us := r.unhealthy[svcName]

		for _, node := range nodes {
			if _, exists := r.nodeMap[node]; !exists {
				r.nodeMap[node] = &nodeContext{
					serviceName: svcName,
					forwarder:   forwarder.New(svcName, node, r.failureChan),
				}
			}

			if us == nil || !us.contains(node) {
				healthyNodes = append(healthyNodes, node)
			}
		}

		if len(healthyNodes) > 0 {
			if ss, exists := r.services[svcName]; exists {
				ss.replace(healthyNodes)
			} else {
				r.services[svcName] = newServiceSet(healthyNodes)
			}
		} else {
			delete(r.services, svcName)
		}
		metrics.Global.ServiceNodesActive.WithLabelValues(svcName).Set(float64(len(nodes)))
	}

	return nil
}

// ScanService performs a targeted scan of a single service directory
func (r *Registry) scanService(serviceName string) error {
	serviceDir := filepath.Join(r.baseDir, serviceName)
	var discoveredNodes []string

	err := filepath.WalkDir(serviceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".sock") {
			discoveredNodes = append(discoveredNodes, path)
		}
		return nil
	})

	if err != nil && !os.IsNotExist(err) {
		return err
	}

	discoveredSet := make(map[string]struct{}, len(discoveredNodes))
	for _, node := range discoveredNodes {
		discoveredSet[node] = struct{}{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Identify nodes to remove (healthy and unhealthy)
	// Clone before ranging: removeNodeUnsafe/removeFromUnhealthyUnsafe delete in place, which would skip elements mid-iteration otherwise.
	if currentSet, exists := r.services[serviceName]; exists {
		for _, oldNode := range slices.Clone(currentSet.nodes) {
			if _, found := discoveredSet[oldNode]; !found {
				r.removeNodeUnsafe(oldNode, false)
				r.removeFromUnhealthyUnsafe(serviceName, oldNode)
			}
		}
	}
	if us, ok := r.unhealthy[serviceName]; ok {
		for _, oldNode := range slices.Clone(us.nodes) {
			if _, found := discoveredSet[oldNode]; !found {
				r.removeNodeUnsafe(oldNode, false)
				r.removeFromUnhealthyUnsafe(serviceName, oldNode)
			}
		}
	}

	// 2. Identify and add new nodes
	if len(discoveredNodes) == 0 {
		delete(r.services, serviceName)
		metrics.Global.ServiceNodesActive.WithLabelValues(serviceName).Set(0)
		return nil
	}

	us := r.unhealthy[serviceName]
	finalHealthyNodes := make([]string, 0, len(discoveredNodes))
	for _, node := range discoveredNodes {
		if _, exists := r.nodeMap[node]; !exists {
			r.nodeMap[node] = &nodeContext{
				serviceName: serviceName,
				forwarder:   forwarder.New(serviceName, node, r.failureChan),
			}
		}

		if us == nil || !us.contains(node) {
			finalHealthyNodes = append(finalHealthyNodes, node)
		}
	}

	// 3. Update service set
	if len(finalHealthyNodes) > 0 {
		if ss, exists := r.services[serviceName]; exists {
			ss.replace(finalHealthyNodes)
		} else {
			r.services[serviceName] = newServiceSet(finalHealthyNodes)
		}
	} else {
		delete(r.services, serviceName)
	}
	metrics.Global.ServiceNodesActive.WithLabelValues(serviceName).Set(float64(len(discoveredNodes)))

	return nil
}

func (r *Registry) RemoveNode(nodePath string, shouldDeleteFile bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeNodeUnsafe(nodePath, shouldDeleteFile)
}

func (r *Registry) removeNodeUnsafe(nodePath string, shouldDeleteFile bool) {
	ctx, ok := r.nodeMap[nodePath]
	if !ok {
		return
	}

	serviceName := ctx.serviceName
	delete(r.nodeMap, nodePath)

	if ss, ok := r.services[serviceName]; ok {
		ss.remove(nodePath)
		metrics.Global.ServiceNodesActive.WithLabelValues(serviceName).Set(float64(len(ss.nodes)))
		if len(ss.nodes) == 0 {
			delete(r.services, serviceName)
		}
	}

	if shouldDeleteFile {
		go os.Remove(nodePath)
	}
}

func (r *Registry) RetryUnhealthy() {
	r.mu.Lock()
	toCheck := make(map[string][]string)
	for svcName, ss := range r.unhealthy {
		if len(ss.nodes) > 0 {
			toCheck[svcName] = slices.Clone(ss.nodes)
		}
	}
	r.mu.Unlock()

	for svcName, nodes := range toCheck {
		for _, nodePath := range nodes {
			r.mu.RLock()
			ctx, ok := r.nodeMap[nodePath]
			r.mu.RUnlock()

			if !ok {
				r.mu.Lock()
				r.removeFromUnhealthyUnsafe(svcName, nodePath)
				r.mu.Unlock()
				continue
			}

			if err := ctx.forwarder.TryReconnect(); err == nil {
				logs.Out.Info("Node recovered",
					zap.String("service", svcName),
					zap.String("socketPath", nodePath))

				r.mu.Lock()
				r.promoteToHealthyUnsafe(svcName, nodePath)
				r.mu.Unlock()
			} else if errors.Is(err, syscall.ENOENT) {
				logs.Out.Info("Node socket removed, cleaning up",
					zap.String("service", svcName),
					zap.String("socketPath", nodePath))

				r.mu.Lock()
				r.removeNodeUnsafe(nodePath, false)
				r.removeFromUnhealthyUnsafe(svcName, nodePath)
				r.mu.Unlock()
			} else {
				logs.Out.Debug("Node still unhealthy",
					zap.String("service", svcName),
					zap.String("socketPath", nodePath),
					zap.Error(err))
			}
		}
	}
}

func (r *Registry) promoteToHealthyUnsafe(serviceName, nodePath string) {
	r.removeFromUnhealthyUnsafe(serviceName, nodePath)

	ss, ok := r.services[serviceName]
	if !ok {
		ss = newServiceSet(nil)
		r.services[serviceName] = ss
	}

	ss.add(nodePath)

	metrics.Global.ServiceNodesActive.WithLabelValues(serviceName).Set(float64(len(ss.nodes)))
}

func (r *Registry) removeFromUnhealthyUnsafe(serviceName, nodePath string) {
	if ss, ok := r.unhealthy[serviceName]; ok {
		ss.remove(nodePath)
		if len(ss.nodes) == 0 {
			delete(r.unhealthy, serviceName)
		}
	}
}
