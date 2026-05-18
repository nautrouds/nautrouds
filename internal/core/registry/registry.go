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
	nodes []string
	index uint32
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
		for i, n := range ss.nodes {
			if n == nodePath {
				ss.nodes = slices.Delete(ss.nodes, i, i+1)
				break
			}
		}
		if len(ss.nodes) == 0 {
			delete(r.services, serviceName)
		}
		metrics.Global.ServiceNodesActive.WithLabelValues(serviceName).Set(float64(len(ss.nodes)))
	}

	if _, ok := r.unhealthy[serviceName]; !ok {
		r.unhealthy[serviceName] = &ServiceSet{}
	}

	if !slices.Contains(r.unhealthy[serviceName].nodes, nodePath) {
		r.unhealthy[serviceName].nodes = append(r.unhealthy[serviceName].nodes, nodePath)
	}
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

	idx := atomic.AddUint32(&ss.index, 1) % uint32(len(ss.nodes))
	nodePath := ss.nodes[idx]

	r.mu.RLock()
	ctx, ok := r.nodeMap[nodePath]
	r.mu.RUnlock()

	if !ok {
		return nil, os.ErrNotExist
	}

	return ctx.forwarder, nil
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
		discovered, found := scannedState[ctx.serviceName]
		if !found || !slices.Contains(discovered, path) {
			r.removeNodeUnsafe(path, false)
			r.removeFromUnhealthyUnsafe(ctx.serviceName, path)
		}
	}

	// 2. Add new nodes and update services
	for svcName, nodes := range scannedState {
		healthyNodes := make([]string, 0)

		for _, node := range nodes {
			if _, exists := r.nodeMap[node]; !exists {
				r.nodeMap[node] = &nodeContext{
					serviceName: svcName,
					forwarder:   forwarder.New(svcName, node, r.failureChan),
				}
			}

			isUnhealthy := false
			if us, ok := r.unhealthy[svcName]; ok {
				if slices.Contains(us.nodes, node) {
					isUnhealthy = true
				}
			}

			if !isUnhealthy {
				healthyNodes = append(healthyNodes, node)
			}
		}

		if len(healthyNodes) > 0 {
			if ss, exists := r.services[svcName]; exists {
				ss.nodes = healthyNodes
			} else {
				r.services[svcName] = &ServiceSet{nodes: healthyNodes}
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

	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Identify nodes to remove
	currentSet, exists := r.services[serviceName]
	if exists {
		for _, oldNode := range currentSet.nodes {
			if !slices.Contains(discoveredNodes, oldNode) {
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

	finalHealthyNodes := make([]string, 0)
	for _, node := range discoveredNodes {
		if _, exists := r.nodeMap[node]; !exists {
			r.nodeMap[node] = &nodeContext{
				serviceName: serviceName,
				forwarder:   forwarder.New(serviceName, node, r.failureChan),
			}
		}

		isUnhealthy := false
		if us, ok := r.unhealthy[serviceName]; ok {
			if slices.Contains(us.nodes, node) {
				isUnhealthy = true
			}
		}

		if !isUnhealthy {
			finalHealthyNodes = append(finalHealthyNodes, node)
		}
	}

	// 3. Update service set
	if len(finalHealthyNodes) > 0 {
		if ss, exists := r.services[serviceName]; exists {
			ss.nodes = finalHealthyNodes
		} else {
			r.services[serviceName] = &ServiceSet{nodes: finalHealthyNodes}
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
		for i, n := range ss.nodes {
			if n == nodePath {
				ss.nodes = slices.Delete(ss.nodes, i, i+1)
				break
			}
		}
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
		ss = &ServiceSet{}
		r.services[serviceName] = ss
	}

	if !slices.Contains(ss.nodes, nodePath) {
		ss.nodes = append(ss.nodes, nodePath)
	}

	metrics.Global.ServiceNodesActive.WithLabelValues(serviceName).Set(float64(len(ss.nodes)))
}

func (r *Registry) removeFromUnhealthyUnsafe(serviceName, nodePath string) {
	if ss, ok := r.unhealthy[serviceName]; ok {
		for i, n := range ss.nodes {
			if n == nodePath {
				ss.nodes = slices.Delete(ss.nodes, i, i+1)
				break
			}
		}
		if len(ss.nodes) == 0 {
			delete(r.unhealthy, serviceName)
		}
	}
}
