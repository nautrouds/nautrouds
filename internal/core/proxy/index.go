package proxy

import (
	"bufio"
	"context"
	"fmt"
	"nautrouds/internal/core/builtins"
	"nautrouds/internal/core/builtins/builtinsmware"
	"nautrouds/internal/core/builtins/virtualservices"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/metrics"
	"nautrouds/internal/core/registry"
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

// responseState wraps http.ResponseWriter to track activity, status code, and response size.
type responseState struct {
	http.ResponseWriter
	status int
	size   int64
}

func (rs *responseState) WriteHeader(code int) {
	rs.status = code
	rs.ResponseWriter.WriteHeader(code)
}

func (rs *responseState) Write(b []byte) (int, error) {
	n, err := rs.ResponseWriter.Write(b)
	rs.size += int64(n)
	return n, err
}

func (rs *responseState) Flush() {
	if f, ok := rs.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rs *responseState) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rs.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
}

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

// resolveBuiltinMiddleware parses and caches functional middleware expressions.
func (m *Manager) resolveBuiltinMiddleware(expr string) builtinsmware.HandlerFunc {
	if h, ok := m.builtinCache.Load(expr); ok {
		return h.(builtinsmware.HandlerFunc)
	}

	funcName, args, err := builtins.ParseDirective(expr)
	if err != nil {
		return nil
	}

	if factory, ok := builtinsmware.Registry[funcName]; ok {
		handler := factory(args...)
		m.builtinCache.Store(expr, handler)
		return handler
	}

	return nil
}

func (m *Manager) resolveExternalMiddleware(expr string) (string, string, error) {
	if h, ok := m.builtinCache.Load(expr); ok {
		values := h.([]string)
		return values[0], values[1], nil
	}

	funcName, args, err := builtins.ParseDirective(expr)
	if err != nil {
		return "", "", err
	}

	path := ""
	if len(args) > 0 {
		path = args[0]
	}

	m.builtinCache.Store(expr, []string{funcName, path})
	return funcName, path, nil
}

// resolveVirtualService parses and caches functional virtual service expressions.
func (m *Manager) resolveVirtualService(expr string) http.HandlerFunc {
	// Special case for $services which needs access to registry state
	if expr == "$services" {
		return virtualservices.Discovery(m.Registry.GetState())
	}

	funcName, args, err := builtins.ParseDirective(expr)
	if err != nil {
		return nil
	}

	// Special case for $ping which needs access to registry and arguments
	if funcName == "$ping" {
		targetSvc := ""
		if len(args) > 0 {
			targetSvc = args[0]
		}
		nodes := m.Registry.GetNodes(targetSvc)
		return virtualservices.Ping(targetSvc, nodes)
	}

	if h, ok := m.virtualCache.Load(expr); ok {
		return h.(http.HandlerFunc)
	}

	if factory, ok := virtualservices.Registry[funcName]; ok {
		handler := factory(args...)
		m.virtualCache.Store(expr, handler)
		return handler
	}

	return nil
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	metrics.Global.IncRequests()
	metrics.Global.AddActive(1)
	defer metrics.Global.AddActive(-1)

	// Wrap ResponseWriter to track activity, status, and size
	trackedWriter := &responseState{ResponseWriter: w, status: http.StatusOK}

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
	lookupPath := host + r.URL.Path
	node, exists := tree.Search(rtree.ReverseHost([]byte(lookupPath)))
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
	// 3. Middleware Execution
	for _, mwExpr := range resolvedMiddlewares {

		if strings.HasPrefix(mwExpr, "$") {
			// Internal built-in middleware logic
			if handler := m.resolveBuiltinMiddleware(mwExpr); handler != nil {
				resp := builtinsmware.NewResponseWriter()
				handler(resp, r)
				if resp.GetCode() != http.StatusOK {
					resp.WriteTo(trackedWriter)
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

			mw, err := m.Registry.GetForwarder(funcName)
			if err != nil {
				logs.Out.Error("Middleware Resolution Failed", zap.Error(err), zap.String("expr", mwExpr))
				http.Error(trackedWriter, ErrInternal, http.StatusInternalServerError)
				return
			}

			resp := builtinsmware.NewResponseWriter()
			// External middleware (usually over UDS)

			if !mw.ForwardMiddleware(resp, r, path) {
				// If forwarding fails or is intercepted, stop execution
				resp.WriteTo(trackedWriter)
				return
			}
		}

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

	// 5. Load Balancing (Protected by RLock)
	service, err := m.Registry.GetForwarder(finalServiceName)
	if err != nil {
		logs.Out.Warn("Backend Service Unavailable", zap.String("service", finalServiceName), zap.Error(err))
		http.Error(trackedWriter, ErrServiceUnav, http.StatusServiceUnavailable)
		return
	}
	service.Forward(trackedWriter, r)
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
