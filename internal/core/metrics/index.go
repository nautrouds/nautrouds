package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry wraps Prometheus collectors to maintain backward compatibility
// and provide a central point for metrics management.
type Registry struct {
	handler http.Handler

	// Existing metrics (legacy support)
	requestsTotal  prometheus.Counter
	errorsTotal    prometheus.Counter
	configUpdates  prometheus.Counter
	activeRequests prometheus.Gauge
	uptimeSeconds  prometheus.Collector

	// New performance and latency metrics
	RequestDuration  *prometheus.HistogramVec
	UpstreamDuration *prometheus.HistogramVec

	// Detailed traffic analysis
	HTTPRequestsTotal  *prometheus.CounterVec
	RequestBytesTotal  *prometheus.CounterVec
	ResponseBytesTotal *prometheus.CounterVec

	// Service registration and health
	ServiceNodesActive   *prometheus.GaugeVec
	NodeFailuresTotal    *prometheus.CounterVec
	RegistryScanDuration prometheus.Histogram

	// System and configuration
	ConfigReloadDuration prometheus.Histogram
	ConfigErrorsTotal    *prometheus.CounterVec

	// Tentacle metrics (Remote push)
	TentacleActiveConnections       *prometheus.GaugeVec
	TentacleConnectionAttemptsTotal *prometheus.CounterVec
	TentacleConnectionFailuresTotal *prometheus.CounterVec
	TentacleBytesTransmittedTotal   *prometheus.CounterVec
	TentacleTransportLatency        *prometheus.HistogramVec
}

var (
	// Global is the default registry instance
	Global *Registry

	// DefaultBuckets for histograms
	DefaultBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}
)

func init() {
	Global = NewRegistry()
}

// NewRegistry creates and registers a new Prometheus-backed registry
func NewRegistry() *Registry {
	startTime := time.Now()

	r := &Registry{
		requestsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nautrouds_requests_total",
			Help: "Total number of processed requests",
		}),
		errorsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nautrouds_errors_total",
			Help: "Total number of failed requests",
		}),
		configUpdates: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nautrouds_config_updates_total",
			Help: "Total number of configuration swaps",
		}),
		activeRequests: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nautrouds_active_requests",
			Help: "Number of requests currently being processed",
		}),
		uptimeSeconds: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "nautrouds_uptime_seconds",
			Help: "Engine uptime in seconds",
		}, func() float64 {
			return time.Since(startTime).Seconds()
		}),

		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "nautrouds_request_duration_seconds",
			Help:    "Request processing time in seconds",
			Buckets: DefaultBuckets,
		}, []string{"method", "route"}),

		UpstreamDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "nautrouds_upstream_duration_seconds",
			Help:    "Upstream response time in seconds",
			Buckets: DefaultBuckets,
		}, []string{"service", "node"}),

		HTTPRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nautrouds_http_requests_total",
			Help: "Total number of HTTP requests",
		}, []string{"service", "status", "method"}),

		RequestBytesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nautrouds_request_bytes_total",
			Help: "Total number of bytes received",
		}, []string{"service"}),

		ResponseBytesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nautrouds_response_bytes_total",
			Help: "Total number of bytes sent",
		}, []string{"service"}),

		ServiceNodesActive: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "nautrouds_service_nodes_active",
			Help: "Number of active UDS nodes per service",
		}, []string{"service"}),

		NodeFailuresTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nautrouds_node_failures_total",
			Help: "Total number of UDS connection failures",
		}, []string{"service", "node"}),

		RegistryScanDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "nautrouds_registry_scan_duration_seconds",
			Help:    "UDS directory scan duration in seconds",
			Buckets: DefaultBuckets,
		}),

		ConfigReloadDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "nautrouds_config_reload_duration_seconds",
			Help:    "Configuration hot-reload duration in seconds",
			Buckets: DefaultBuckets,
		}),

		ConfigErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nautrouds_config_errors_total",
			Help: "Total number of configuration errors",
		}, []string{"type"}),

		TentacleActiveConnections: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "tentacle_active_connections",
			Help: "Current active TCP backend connections.",
		}, []string{"tentacle_id", "service"}),

		TentacleConnectionAttemptsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tentacle_connection_attempts_total",
			Help: "Total backend connection attempts.",
		}, []string{"tentacle_id", "service"}),

		TentacleConnectionFailuresTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tentacle_connection_failures_total",
			Help: "Total backend connection failures.",
		}, []string{"tentacle_id", "service"}),

		TentacleBytesTransmittedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tentacle_bytes_transmitted_total",
			Help: "Total bytes transferred (bidirectional).",
		}, []string{"tentacle_id", "service"}),

		TentacleTransportLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "tentacle_transport_latency_seconds",
			Help:    "UDS-to-TCP bridge transmission latency.",
			Buckets: DefaultBuckets,
		}, []string{"tentacle_id", "service"}),
	}

	// Register all collectors
	prometheus.MustRegister(
		r.requestsTotal,
		r.errorsTotal,
		r.configUpdates,
		r.activeRequests,
		r.uptimeSeconds,
		r.RequestDuration,
		r.UpstreamDuration,
		r.HTTPRequestsTotal,
		r.RequestBytesTotal,
		r.ResponseBytesTotal,
		r.ServiceNodesActive,
		r.NodeFailuresTotal,
		r.RegistryScanDuration,
		r.ConfigReloadDuration,
		r.ConfigErrorsTotal,
		r.TentacleActiveConnections,
		r.TentacleConnectionAttemptsTotal,
		r.TentacleConnectionFailuresTotal,
		r.TentacleBytesTransmittedTotal,
		r.TentacleTransportLatency,
	)

	// Cache the handler
	r.handler = promhttp.Handler()

	return r
}

// Legacy methods for backward compatibility
func (r *Registry) IncRequests()      { r.requestsTotal.Inc() }
func (r *Registry) IncErrors()        { r.errorsTotal.Inc() }
func (r *Registry) IncUpdates()       { r.configUpdates.Inc() }
func (r *Registry) AddActive(n int64) { r.activeRequests.Add(float64(n)) }

// WritePrometheus writes metrics in Prometheus format to the response writer
func (r *Registry) WritePrometheus(w http.ResponseWriter, req *http.Request) {
	r.handler.ServeHTTP(w, req)
}
