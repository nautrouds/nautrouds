package proxy

import (
	"nautrouds/internal/core/builtins/builtinsmware"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/registry/forwarder"
	"nautrouds/internal/interpolate"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

func (m *Manager) runMiddlewareChain(s *servingState) bool {
	mwCount := s.tree.ActionMetadata[s.node.ActionIndex+1]
	if mwCount == 0 {
		return false
	}

	baseOffset := s.node.ActionIndex + 2
	for i := range mwCount {
		mwMetaIndex := s.tree.ActionMetadata[baseOffset+i]
		rawMwName := s.tree.GetActionName(s.tree.ActionMetadata[mwMetaIndex])
		opLen := s.tree.ActionMetadata[mwMetaIndex+1]

		mwExpr := rawMwName
		isStatic := opLen == 0
		if !isStatic {
			opOffset := mwMetaIndex + 2
			ops := s.tree.ActionMetadata[opOffset : opOffset+opLen]
			if s.interpolator == nil {
				s.interpolator = interpolate.New(s.r)
			}
			mwExpr = s.interpolator.Replace(rawMwName, ops)
		}

		s.tempResp.Setup(s.w)

		if strings.HasPrefix(mwExpr, "$") {
			var handler builtinsmware.HandlerFunc
			if isStatic {
				handler = s.state.builtins[mwExpr]
			} else {
				handler = buildBuiltinHandler(mwExpr)
			}
			if handler == nil {
				logs.Out.Error("Failed To Resolve Built-in Middleware", zap.String("expr", mwExpr))
				http.Error(s.w, ErrInternal, http.StatusInternalServerError)
				return true
			}
			handler(s.tempResp, s.r)
			if s.tempResp.GetCode() != http.StatusOK {
				if !s.tempResp.IsPassthrough() {
					s.tempResp.WriteTo(s.w)
				}
				return true
			}
		} else {
			var ext ExternalMW
			if isStatic {
				ext = s.state.externals[mwExpr]
			} else {
				ext = buildExternalMW(mwExpr)
			}

			mwNodes := m.Registry.GetForwarders(ext.FuncName)
			approved := false
			for _, mw := range mwNodes {
				mwErr := mw.ForwardMiddleware(s.tempResp, s.r, ext.Path, ext.AllowedHeaders)
				if mwErr == nil {
					approved = true
					break
				}
				if mwErr == forwarder.ErrNodeUnavailable {
					continue
				}
				// Middleware intentionally blocked — response already written to tempResp.
				if !s.tempResp.IsPassthrough() {
					s.tempResp.WriteTo(s.w)
				}
				return true
			}
			if !approved {
				logs.Out.Warn("Middleware Service Unavailable", zap.String("expr", mwExpr))
				http.Error(s.w, ErrServiceUnav, http.StatusServiceUnavailable)
				return true
			}
		}

		s.tempResp.Reset()
	}

	return false
}
