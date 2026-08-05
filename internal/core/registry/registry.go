package registry

import (
	"errors"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/metrics"
	"nautrouds/internal/core/registry/forwarder"
	"nautrouds/internal/core/registry/loadbalance"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"

	"go.uber.org/zap"
)

type Registry struct {
	mu sync.RWMutex

	services  map[string]*ServiceSet
	unhealthy map[string]*ServiceSet
	nodeMap   map[string]*nodeContext

	failureChan chan forwarder.FailureForwarder
}

type ServiceSet struct {
	nodes         []string
	nodePositions map[string]int
	roundRobin    atomic.Pointer[loadbalance.RoundRobin]
	leastInFlight atomic.Pointer[loadbalance.LeastInFlight]
	strategy      loadbalance.Strategy
}

func newServiceSet(nodes []string, strategy loadbalance.Strategy) *ServiceSet {
	ss := &ServiceSet{strategy: strategy}
	ss.replace(nodes)
	return ss
}

func (ss *ServiceSet) ensureNodePositions() {
	if ss.nodePositions != nil {
		return
	}
	ss.nodePositions = make(map[string]int, len(ss.nodes))
	for i, n := range ss.nodes {
		ss.nodePositions[n] = i
	}
}

func resolveForwarders(nodes []string, nodeMap map[string]*nodeContext) []*forwarder.Forwarder {
	forwarders := make([]*forwarder.Forwarder, 0, len(nodes))
	for _, node := range nodes {
		if ctx, ok := nodeMap[node]; ok {
			forwarders = append(forwarders, ctx.forwarder)
		}
	}
	return forwarders
}

func buildRoundRobin(nodes []string, nodeMap map[string]*nodeContext) *loadbalance.RoundRobin {
	return loadbalance.NewRoundRobin(resolveForwarders(nodes, nodeMap))
}

func buildLeastInFlight(nodes []string, nodeMap map[string]*nodeContext) *loadbalance.LeastInFlight {
	return loadbalance.NewLeastInFlight(resolveForwarders(nodes, nodeMap))
}

func rebuildLoadBalancer(ss *ServiceSet, nodeMap map[string]*nodeContext) {
	switch ss.strategy {
	case loadbalance.StrategyLeastInFlight, loadbalance.StrategyP2C:
		ss.leastInFlight.Store(buildLeastInFlight(ss.nodes, nodeMap))
	default:
		ss.roundRobin.Store(buildRoundRobin(ss.nodes, nodeMap))
	}
}

func (ss *ServiceSet) replace(nodes []string) {
	ss.nodes = nodes
	ss.nodePositions = make(map[string]int, len(nodes))
	for i, n := range nodes {
		ss.nodePositions[n] = i
	}
}

func (ss *ServiceSet) contains(node string) bool {
	ss.ensureNodePositions()
	_, ok := ss.nodePositions[node]
	return ok
}

func (ss *ServiceSet) add(node string) bool {
	ss.ensureNodePositions()
	if _, ok := ss.nodePositions[node]; ok {
		return false
	}
	ss.nodePositions[node] = len(ss.nodes)
	ss.nodes = append(ss.nodes, node)
	return true
}

func (ss *ServiceSet) remove(node string) bool {
	ss.ensureNodePositions()
	removedIdx, ok := ss.nodePositions[node]
	if !ok {
		return false
	}
	lastIdx := len(ss.nodes) - 1
	lastNode := ss.nodes[lastIdx]
	ss.nodes[removedIdx] = lastNode
	ss.nodes = ss.nodes[:lastIdx]
	if lastNode != node {
		ss.nodePositions[lastNode] = removedIdx
	}
	delete(ss.nodePositions, node)
	return true
}

type nodeContext struct {
	serviceName string
	forwarder   *forwarder.Forwarder
}

func newForwarder(serviceName, node string, failureChan chan forwarder.FailureForwarder) *forwarder.Forwarder {
	weight, ok := loadbalance.ParseNodeWeight(node)
	if !ok {
		logs.Out.Warn("Invalid node weight suffix, defaulting to 1",
			zap.String("service", serviceName), zap.String("path", node))
	}
	return forwarder.New(serviceName, node, weight, failureChan)
}

func NewRegistry() (*Registry, error) {
	r := &Registry{
		services:    make(map[string]*ServiceSet),
		unhealthy:   make(map[string]*ServiceSet),
		nodeMap:     make(map[string]*nodeContext),
		failureChan: make(chan forwarder.FailureForwarder, 100),
	}

	go r.listenFailures()

	return r, nil
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
		rebuildLoadBalancer(ss, r.nodeMap)
		if len(ss.nodes) == 0 {
			delete(r.services, serviceName)
		}
		metrics.Global.ServiceNodesActive.WithLabelValues(serviceName).Set(float64(len(ss.nodes)))
	}

	us, ok := r.unhealthy[serviceName]
	if !ok {
		us = newServiceSet(nil, loadbalance.StrategyRoundRobin)
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

// GetForwarders returns all healthy forwarders for a service, ordered starting
// from the round-robin position, then reordered per ss.strategy. The index
// advances only once per call so that retry loops within a single request
// visit each node exactly once.
func (r *Registry) GetForwarders(serviceName string) []*forwarder.Forwarder {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ss, exists := r.services[serviceName]
	if !exists || len(ss.nodes) == 0 {
		return nil
	}

	switch ss.strategy {
	case loadbalance.StrategyLeastInFlight:
		return ss.leastInFlight.Load().Get()
	case loadbalance.StrategyP2C:
		return ss.leastInFlight.Load().GetP2C()
	default:
		return ss.roundRobin.Load().Get()
	}
}

func (*Registry) Extension() string {
	return ".sock"
}

func (r *Registry) ApplyFullScan(baseDir string, byService map[string]map[string]struct{}) error {
	for path, ctx := range r.nodeMap {
		if _, ok := byService[ctx.serviceName][path]; !ok {
			r.removeNodeUnsafe(path, false)
			r.removeFromUnhealthyUnsafe(ctx.serviceName, path)
		}
	}

	// 2. Add new nodes and update services
	for svcName, nodes := range byService {
		healthyNodes := make([]string, 0, len(nodes))
		us := r.unhealthy[svcName]

		for node := range nodes {
			if _, exists := r.nodeMap[node]; !exists {
				r.nodeMap[node] = &nodeContext{
					serviceName: svcName,
					forwarder:   newForwarder(svcName, node, r.failureChan),
				}
			}

			if us == nil || !us.contains(node) {
				healthyNodes = append(healthyNodes, node)
			}
		}

		if len(healthyNodes) > 0 {
			var ss *ServiceSet
			if existing, exists := r.services[svcName]; exists {
				existing.replace(healthyNodes)
				ss = existing
			} else {
				strategy := readStrategyAtCreation(baseDir, svcName)
				ss = newServiceSet(healthyNodes, strategy)
				r.services[svcName] = ss
			}
			rebuildLoadBalancer(ss, r.nodeMap)
		} else {
			delete(r.services, svcName)
		}
		metrics.Global.ServiceNodesActive.WithLabelValues(svcName).Set(float64(len(nodes)))
	}

	return nil
}

func (r *Registry) ApplyServiceScan(baseDir string, serviceName string, discovered []string) error {

	discoveredSet := make(map[string]struct{}, len(discovered))
	for _, node := range discovered {
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
	if len(discovered) == 0 {
		delete(r.services, serviceName)
		metrics.Global.ServiceNodesActive.WithLabelValues(serviceName).Set(0)
		return nil
	}

	us := r.unhealthy[serviceName]
	finalHealthyNodes := make([]string, 0, len(discovered))
	for _, node := range discovered {
		if _, exists := r.nodeMap[node]; !exists {
			r.nodeMap[node] = &nodeContext{
				serviceName: serviceName,
				forwarder:   newForwarder(serviceName, node, r.failureChan),
			}
		}

		if us == nil || !us.contains(node) {
			finalHealthyNodes = append(finalHealthyNodes, node)
		}
	}

	// 3. Update service set
	if len(finalHealthyNodes) > 0 {
		var ss *ServiceSet
		if existing, exists := r.services[serviceName]; exists {
			existing.replace(finalHealthyNodes)
			ss = existing
		} else {
			strategy := readStrategyAtCreation(baseDir, serviceName)
			ss = newServiceSet(finalHealthyNodes, strategy)
			r.services[serviceName] = ss
		}
		rebuildLoadBalancer(ss, r.nodeMap)
	} else {
		delete(r.services, serviceName)
	}
	metrics.Global.ServiceNodesActive.WithLabelValues(serviceName).Set(float64(len(discovered)))

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
		rebuildLoadBalancer(ss, r.nodeMap)
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
		ss = newServiceSet(nil, loadbalance.StrategyRoundRobin)
		r.services[serviceName] = ss
	}

	ss.add(nodePath)
	rebuildLoadBalancer(ss, r.nodeMap)

	metrics.Global.ServiceNodesActive.WithLabelValues(serviceName).Set(float64(len(ss.nodes)))
}

func (r *Registry) setStrategy(serviceName string, s loadbalance.Strategy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ss, ok := r.services[serviceName]
	if !ok {
		return
	}
	ss.strategy = s
	rebuildLoadBalancer(ss, r.nodeMap)
}

func (r *Registry) serviceNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.services))
	for name := range r.services {
		names = append(names, name)
	}
	return names
}

func (r *Registry) removeFromUnhealthyUnsafe(serviceName, nodePath string) {
	if ss, ok := r.unhealthy[serviceName]; ok {
		ss.remove(nodePath)
		if len(ss.nodes) == 0 {
			delete(r.unhealthy, serviceName)
		}
	}
}
