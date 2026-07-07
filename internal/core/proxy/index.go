package proxy

import (
	"context"
	"fmt"
	"nautrouds/internal/core/builtins/builtinsmware"
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
	State    atomic.Pointer[Generation]
	Registry *registry.Registry
}

func NewManager(reg *registry.Registry) *Manager {
	m := &Manager{
		Registry: reg,
	}
	return m
}

func (m *Manager) UpdateGeneration(gen *Generation) {
	gen.InitCaches()
	m.State.Store(gen)
	metrics.Global.IncUpdates()
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	metrics.Global.IncRequests()
	metrics.Global.AddActive(1)
	defer metrics.Global.AddActive(-1)

	// Wrap ResponseWriter to track activity, status, and size
	trackedWriter := newTrackedResponseWriter(w)
	defer trackedWriter.release()

	var finalServiceName string
	var routePattern string

	defer func() {
		if trackedWriter.hijacked {
			return
		}
		duration := time.Since(start).Seconds()
		metrics.Global.RequestDuration.WithLabelValues(r.Method, routePattern).Observe(duration)
	}()

	state := m.State.Load()
	tree := &state.Tree

	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}

	// 1. Route Lookup
	lookupPath := host + r.URL.Path
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

	var interpolator *interpolate.RequestContext

	serviceMetadataIndex := tree.ActionMetadata[node.ActionIndex]
	targetServiceID := tree.ActionMetadata[serviceMetadataIndex]
	rawServiceName := tree.GetActionName(targetServiceID)

	tempResp := tempresp.Pool.Get().(*tempresp.ResponseWriter)
	defer tempresp.Pool.Put(tempResp)

	// 3. Middleware Execution
	mwCount := tree.ActionMetadata[node.ActionIndex+1]
	if mwCount > 0 {
		baseOffset := node.ActionIndex + 2
		for i := range mwCount {
			mwMetaIndex := tree.ActionMetadata[baseOffset+i]
			rawMwName := tree.GetActionName(tree.ActionMetadata[mwMetaIndex])
			opLen := tree.ActionMetadata[mwMetaIndex+1]

			mwExpr := rawMwName
			isStatic := opLen == 0
			if !isStatic {
				opOffset := mwMetaIndex + 2
				ops := tree.ActionMetadata[opOffset : opOffset+opLen]
				if interpolator == nil {
					interpolator = interpolate.New(r)
				}
				mwExpr = interpolator.Replace(rawMwName, ops)
			}

			tempResp.Setup(trackedWriter)

			if strings.HasPrefix(mwExpr, "$") {
				var handler builtinsmware.HandlerFunc
				if isStatic {
					handler = state.builtins[mwExpr]
				} else {
					handler = buildBuiltinHandler(mwExpr)
				}
				if handler == nil {
					logs.Out.Error("Failed To Resolve Built-in Middleware", zap.String("expr", mwExpr))
					http.Error(trackedWriter, ErrInternal, http.StatusInternalServerError)
					return
				}
				handler(tempResp, r)
				if tempResp.GetCode() != http.StatusOK {
					if !tempResp.IsPassthrough() {
						tempResp.WriteTo(trackedWriter)
					}
					return
				}
			} else {
				var ext ExternalMW
				if isStatic {
					ext = state.externals[mwExpr]
				} else {
					ext = buildExternalMW(mwExpr)
				}

				mwNodes := m.Registry.GetForwarders(ext.FuncName)
				approved := false
				for _, mw := range mwNodes {
					mwErr := mw.ForwardMiddleware(tempResp, r, ext.Path, ext.AllowedHeaders)
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
	}

	finalServiceName = rawServiceName
	isStaticService := true
	if opCount := tree.ActionMetadata[serviceMetadataIndex+1]; opCount > 0 {
		offset := serviceMetadataIndex + 2
		ops := tree.ActionMetadata[offset : offset+opCount]
		if interpolator == nil {
			interpolator = interpolate.New(r)
		}
		finalServiceName = interpolator.Replace(rawServiceName, ops)
		isStaticService = false
	}

	// 4. Virtual Service Check
	if strings.HasPrefix(finalServiceName, "$") {
		handler := m.resolveSpecialVirtualService(finalServiceName) // $services/$ping, always live
		if handler == nil {
			if isStaticService {
				handler = state.virtuals[finalServiceName]
			} else {
				handler = buildVirtualHandler(finalServiceName)
			}
		}
		if handler == nil {
			logs.Out.Error("Virtual Service Resolution Failed", zap.String("service", finalServiceName))
			http.Error(trackedWriter, ErrInternal, http.StatusInternalServerError)
			return
		}
		handler(trackedWriter, r)
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
