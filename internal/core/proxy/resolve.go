package proxy

import (
	"nautrouds/internal/core/builtins"
	"nautrouds/internal/core/builtins/virtualservices"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/interpolate"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

func (m *Manager) resolveSpecialVirtualService(expr string) http.HandlerFunc {
	if expr == "$services" {
		return virtualservices.Discovery(m.Registry.GetState())
	}

	if expr != "$ping" && !strings.HasPrefix(expr, "$ping(") {
		return nil
	}

	_, args, err := builtins.ParseDirective(expr)
	if err != nil {
		return nil
	}

	targetSvc := ""
	if len(args) > 0 {
		targetSvc = args[0]
	}
	return virtualservices.Ping(targetSvc, m.Registry.GetNodes(targetSvc))
}

func resolveServiceName(s *servingState) {
	serviceMetadataIndex := s.tree.ActionMetadata[s.node.ActionIndex]
	targetServiceID := s.tree.ActionMetadata[serviceMetadataIndex]
	rawServiceName := s.tree.GetActionName(targetServiceID)

	s.finalServiceName = rawServiceName
	s.isStaticService = true
	if opCount := s.tree.ActionMetadata[serviceMetadataIndex+1]; opCount > 0 {
		offset := serviceMetadataIndex + 2
		ops := s.tree.ActionMetadata[offset : offset+opCount]
		if s.interpolator == nil {
			s.interpolator = interpolate.New(s.r)
		}
		s.finalServiceName = s.interpolator.Replace(rawServiceName, ops)
		s.isStaticService = false
	}
}

func (m *Manager) serveVirtualService(s *servingState) bool {
	if !strings.HasPrefix(s.finalServiceName, "$") {
		return false
	}

	handler := m.resolveSpecialVirtualService(s.finalServiceName) // $services/$ping, always live
	if handler == nil {
		if s.isStaticService {
			handler = s.state.virtuals[s.finalServiceName]
		} else {
			handler = buildVirtualHandler(s.finalServiceName)
		}
	}
	if handler == nil {
		logs.Out.Error("Virtual Service Resolution Failed", zap.String("service", s.finalServiceName))
		http.Error(s.w, ErrInternal, http.StatusInternalServerError)
		return true
	}
	handler(s.w, s.r)
	return true
}
