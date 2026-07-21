package proxy

import (
	"context"
	"fmt"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/metrics"
	"nautrouds/internal/core/mmfg"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/core/tempresp"
	"nautrouds/internal/interpolate"
	"nautrouds/internal/rtree"
	"nautrouds/internal/tags"
	"net"
	"net/http"
	"os"
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
	Mmfg     mmfg.Hub
}

func NewManager(reg *registry.Registry, mmfgHub mmfg.Hub) *Manager {
	m := &Manager{
		Registry: reg,
		Mmfg:     mmfgHub,
	}
	return m
}

func (m *Manager) UpdateGeneration(gen *Generation) {
	gen.InitCaches()
	m.State.Store(gen)
	metrics.Global.IncUpdates()
}

type servingState struct {
	w     http.ResponseWriter
	r     *http.Request
	state *Generation
	tree  *rtree.RouteTree
	node  *rtree.RouteNode

	tempResp     *tempresp.ResponseWriter
	interpolator *interpolate.RequestContext

	routePattern     string
	finalServiceName string
	isStaticService  bool
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	metrics.Global.IncRequests()
	metrics.Global.AddActive(1)
	defer metrics.Global.AddActive(-1)

	// Wrap ResponseWriter to track activity, status, and size
	trackedWriter := newTrackedResponseWriter(w)
	defer trackedWriter.release()

	state := m.State.Load()
	s := &servingState{
		w:     trackedWriter,
		r:     r,
		state: state,
		tree:  &state.Tree,
	}

	defer func() {
		if trackedWriter.hijacked {
			return
		}
		duration := time.Since(start).Seconds()
		metrics.Global.RequestDuration.WithLabelValues(r.Method, s.routePattern).Observe(duration)
	}()

	// Route Lookup
	if !lookupRoute(s) {
		return
	}

	if s.node.Tags&tags.NoMetricsTag == 0 {
		defer func() {
			statusStr := fmt.Sprintf("%d", trackedWriter.status)
			metrics.Global.HTTPRequestsTotal.WithLabelValues(s.finalServiceName, statusStr, r.Method).Inc()
			metrics.Global.ResponseBytesTotal.WithLabelValues(s.finalServiceName).Add(float64(trackedWriter.size))

			if r.ContentLength > 0 {
				metrics.Global.RequestBytesTotal.WithLabelValues(s.finalServiceName).Add(float64(r.ContentLength))
			}
		}()
	}

	// Method Validation
	if !validateMethod(s) {
		return
	}

	s.tempResp = tempresp.Pool.Get().(*tempresp.ResponseWriter)
	defer tempresp.Pool.Put(s.tempResp)

	// Middleware Execution
	if m.runMiddlewareChain(s) {
		return
	}

	// Service Name Resolution
	resolveServiceName(s)

	// Virtual Service Check
	if m.serveVirtualService(s) {
		return
	}

	// Load Balancing
	m.forwardToBackend(s)
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
