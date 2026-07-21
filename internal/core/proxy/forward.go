package proxy

import (
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/registry/forwarder"
	"net/http"

	"go.uber.org/zap"
)

// forwardToBackend — round-robin picks the start; retry walks remaining nodes.
func (m *Manager) forwardToBackend(s *servingState) {
	for _, service := range m.Registry.GetForwarders(s.finalServiceName) {
		if err := service.Forward(s.w, s.r); err != nil {
			if err == forwarder.ErrNodeUnavailable {
				continue
			}
			http.Error(s.w, ErrBadGateway, http.StatusBadGateway)
			return
		}
		return
	}
	logs.Out.Warn("Backend Service Unavailable", zap.String("service", s.finalServiceName))
	http.Error(s.w, ErrServiceUnav, http.StatusServiceUnavailable)
}
