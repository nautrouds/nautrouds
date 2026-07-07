package proxy

import (
	"nautrouds/internal/core/builtins"
	"nautrouds/internal/core/builtins/virtualservices"
	"net/http"
	"strings"
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
