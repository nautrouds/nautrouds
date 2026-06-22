package proxy

import (
	"context"
	"fmt"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/metrics"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/core/registry/forwarder"
	"nautrouds/internal/core/tempresp"
	"nautrouds/internal/interpolate"
	"nautrouds/internal/rtree"
	"nautrouds/internal/tags"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

const (
	ErrInternal    = "Internal Server Error"
	ErrBadGateway  = "Bad Gateway"
	ErrServiceUnav = "Service Unavailable"
)

type Manager struct {
	Tree     atomic.Pointer[rtree.RouteTree]
	Registry *registry.Registry

	middlewareCache sync.Map // map[string]string
	builtinCache    sync.Map // map[string]http.HandlerFunc
	virtualCache    sync.Map // map[string]http.HandlerFunc
}

func NewManager(reg *registry.Registry) *Manager {
	m := &Manager{
		Registry: reg,
	}
	m.Tree.Store(&rtree.RouteTree{})
	return m
}

func (m *Manager) UpdateTree(newTree *rtree.RouteTree) {
	m.Tree.Store(newTree)
	metrics.Global.IncUpdates()
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	metrics.Global.IncRequests()
	metrics.Global.AddActive(1)
	defer metrics.Global.AddActive(-1)

	// Wrap ResponseWriter to track activity, status, and size
	trackedWriter := newTrackedResponseWriter(w)

	var finalServiceName string
	var routePattern string

	defer func() {
		duration := time.Since(start).Seconds()
		metrics.Global.RequestDuration.WithLabelValues(r.Method, routePattern).Observe(duration)
	}()

	tree := m.Tree.Load()

	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}

	// 1. Route Lookup
	lookupPath := fmt.Sprintf("%s%s", host, r.URL.Path)
	lookupPathBytes := []byte(lookupPath)
	rtree.ReverseHost(lookupPathBytes)
	node, exists := tree.Search(lookupPathBytes)
	if !exists {
		routePattern = "404"
		http.Error(trackedWriter, "Resource Not Found", http.StatusNotFound)
		return
	}
	routePattern = lookupPath // Simplified for now, could be node.Pattern if available

	if node.Tags&tags.NoMetricsTag == 0 {
		defer func() {
			statusStr := fmt.Sprintf("%d", trackedWriter.status)
			metrics.Global.HTTPRequestsTotal.WithLabelValues(finalServiceName, statusStr, r.Method).Inc()
			metrics.Global.ResponseBytesTotal.WithLabelValues(finalServiceName).Add(float64(trackedWriter.size))

			if r.ContentLength > 0 {
				metrics.Global.RequestBytesTotal.WithLabelValues(finalServiceName).Add(float64(r.ContentLength))
			}
		}()
	}

	// 2. Method Validation
	methodBit := rtree.HTTPMethodMap[r.Method]
	if methodBit == 0 {
		methodBit = rtree.MethodAny
	}
	if node.Methods&methodBit == 0 {
		http.Error(trackedWriter, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	interpolator := interpolate.New(r)

	serviceMetadataIndex := tree.ActionMetadata[node.ActionIndex]
	targetServiceID := tree.ActionMetadata[serviceMetadataIndex]
	rawServiceName := tree.GetActionName(targetServiceID)

	mwCount := tree.ActionMetadata[node.ActionIndex+1]
	resolvedMiddlewares := make([]string, mwCount)

	if mwCount > 0 {
		baseOffset := node.ActionIndex + 2
		for i := range mwCount {
			mwMetaIndex := tree.ActionMetadata[baseOffset+i]
			rawMwName := tree.GetActionName(tree.ActionMetadata[mwMetaIndex])

			opLen := tree.ActionMetadata[mwMetaIndex+1]
			if opLen > 0 {
				opOffset := mwMetaIndex + 2
				ops := tree.ActionMetadata[opOffset : opOffset+opLen]
				resolvedMiddlewares[i] = interpolator.Replace(rawMwName, ops)
			} else {
				resolvedMiddlewares[i] = rawMwName
			}
		}
	}

	tempResp := tempresp.Pool.Get().(*tempresp.ResponseWriter)
	defer tempresp.Pool.Put(tempResp)

	// 3. Middleware Execution
	for _, mwExpr := range resolvedMiddlewares {
		tempResp.Setup(trackedWriter)

		if strings.HasPrefix(mwExpr, "$") {
			// Internal built-in middleware logic
			if handler := m.resolveBuiltinMiddleware(mwExpr); handler != nil {
				handler(tempResp, r)
				if tempResp.GetCode() != http.StatusOK {
					if !tempResp.IsPassthrough() {
						tempResp.WriteTo(trackedWriter)
					}
					return
				}
			} else {
				logs.Out.Error("Failed To Resolve Built-in Middleware", zap.String("expr", mwExpr))
				http.Error(trackedWriter, ErrInternal, http.StatusInternalServerError)
				return
			}
		} else {
			funcName, path, err := m.resolveExternalMiddleware(mwExpr)
			if err != nil {
				logs.Out.Error("Middleware Resolution Failed", zap.Error(err), zap.String("expr", mwExpr))
				http.Error(trackedWriter, ErrInternal, http.StatusInternalServerError)
				return
			}

			mwNodes := m.Registry.GetForwarders(funcName)
			approved := false
			for _, mw := range mwNodes {
				mwErr := mw.ForwardMiddleware(tempResp, r, path)
				if mwErr == nil {
					approved = true
					break
				}
				if mwErr == forwarder.ErrNodeUnavailable {
					continue
				}
				// Middleware intentionally blocked — response already written to tempResp.
				if !tempResp.IsPassthrough() {
					tempResp.WriteTo(trackedWriter)
				}
				return
			}
			if !approved {
				logs.Out.Warn("Middleware Service Unavailable", zap.String("expr", mwExpr))
				http.Error(trackedWriter, ErrServiceUnav, http.StatusServiceUnavailable)
				return
			}
		}

		tempResp.Reset()
	}

	finalServiceName = rawServiceName
	if opCount := tree.ActionMetadata[serviceMetadataIndex+1]; opCount > 0 {
		offset := serviceMetadataIndex + 2
		ops := tree.ActionMetadata[offset : offset+opCount]
		finalServiceName = interpolator.Replace(rawServiceName, ops)
	}

	// 4. Virtual Service Check
	if strings.HasPrefix(finalServiceName, "$") {
		if handler := m.resolveVirtualService(finalServiceName); handler != nil {
			handler(trackedWriter, r)
			return
		}
		logs.Out.Error("Virtual Service Resolution Failed", zap.String("service", finalServiceName))
		http.Error(trackedWriter, ErrInternal, http.StatusInternalServerError)
		return
	}

	// 5. Load Balancing — round-robin picks the start; retry walks remaining nodes.
	for _, service := range m.Registry.GetForwarders(finalServiceName) {
		if err := service.Forward(trackedWriter, r); err != nil {
			if err == forwarder.ErrNodeUnavailable {
				continue
			}
			http.Error(w, ErrBadGateway, http.StatusBadGateway)
			return
		}
		return
	}
	logs.Out.Warn("Backend Service Unavailable", zap.String("service", finalServiceName))
	http.Error(trackedWriter, ErrServiceUnav, http.StatusServiceUnavailable)
}

func (m *Manager) StartUDSListener(ctx context.Context, socketPath string) error {
	if _, err := os.Stat(socketPath); err == nil {
		if err := os.Remove(socketPath); err != nil {
			return err
		}
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(socketPath, 0666); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	logs.Out.Info("Nautrouds Core listening on UDS", zap.String("socketPath", socketPath))
	return http.Serve(listener, m)
}
