package proxy

import (
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/registry/forwarder"
	"net/http"

	"go.uber.org/zap"
)

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func (m *Manager) forwardToBackend(s *servingState) {
	var retryCount uint64
	for _, service := range m.Registry.GetForwarders(s.finalServiceName) {
		if err := service.Forward(s.w, s.r); err != nil {

			switch err {
			case forwarder.ErrNodeUnavailable:
				if !isSafeMethod(s.r.Method) {
					http.Error(s.w, ErrBadGateway, http.StatusBadGateway)
					return
				}
				if s.options.HasRetryLimit {
					if retryCount >= s.options.RetryLimit {
						http.Error(s.w, ErrBadGateway, http.StatusBadGateway)
						return
					}
					retryCount++
				}
				continue
			case forwarder.ErrNodeFailed:
				continue
			case forwarder.ErrBodyTooLarge:
				http.Error(s.w, ErrRequestTooLarge, http.StatusRequestEntityTooLarge)
				return
			default:
				http.Error(s.w, ErrBadGateway, http.StatusBadGateway)
				return
			}

		}
		return
	}
	logs.Out.Warn("Backend Service Unavailable", zap.String("service", s.finalServiceName))
	http.Error(s.w, ErrServiceUnav, http.StatusServiceUnavailable)
}
