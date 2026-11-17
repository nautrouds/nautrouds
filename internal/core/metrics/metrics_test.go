package metrics

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMetrics(t *testing.T) {
	Global.IncRequests()
	Global.IncErrors()
	Global.IncUpdates()
	Global.AddActive(5)
	Global.AddActive(-2)

	Global.RequestDuration.WithLabelValues("GET", "/test").Observe(0.1)
	Global.HTTPRequestsTotal.WithLabelValues("service-a", "200", "GET").Inc()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	Global.WritePrometheus(w, req)

	body := w.Body.String()

	expectedMetrics := []string{
		"nautrouds_requests_total 1",
		"nautrouds_errors_total 1",
		"nautrouds_config_updates_total 1",
		"nautrouds_active_requests 3",
		"nautrouds_uptime_seconds",
		"nautrouds_request_duration_seconds_bucket",
		"nautrouds_http_requests_total{method=\"GET\",service=\"service-a\",status=\"200\"} 1",
	}

	for _, em := range expectedMetrics {
		assert.Contains(t, body, em)
	}
}
