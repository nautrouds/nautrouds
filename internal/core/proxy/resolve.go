package proxy

import (
	"nautrouds/internal/core/builtins"
	"nautrouds/internal/core/builtins/builtinsmware"
	"nautrouds/internal/core/builtins/virtualservices"
	"net/http"
)

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
