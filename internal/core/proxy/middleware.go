package proxy

import (
	"context"
	"errors"
	"nautrouds/internal/core/builtins/builtinsmware"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/mmfg"
	"nautrouds/internal/core/registry/forwarder"
	"nautrouds/internal/interpolate"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

func (m *Manager) runMiddlewareChain(s *servingState) bool {
	mwCount := s.tree.ActionMetadata[s.node.ActionIndex+1]
	if mwCount == 0 {
		return false
	}

	var mr mmfg.Request

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

		if strings.HasPrefix(mwExpr, "$mmfg(") {
			if mr == nil {

				if !mmfg.IsAvailable {
					logs.Out.Error("mmfg Unavailable", zap.String("expr", mwExpr))
					http.Error(s.w, ErrInternal, http.StatusInternalServerError)
					return true
				}

				ctx, cancel := context.WithTimeout(s.r.Context(), time.Second*1)
				defer cancel()
				req, err := m.Mmfg.Request(ctx, s.r)
				if err != nil {
					logs.Out.Error("mmfg Request Error", zap.String("expr", mwExpr), zap.Error(err))
					http.Error(s.w, ErrInternal, http.StatusInternalServerError)
					return true
				}
				mr = req
			}

			node := mwExpr[6 : len(mwExpr)-1]
			selfRespond, err := mr.Next(node)
			if err != nil {
				logs.Out.Error("mmfg Next Error", zap.String("node", node), zap.Error(err))
				http.Error(s.w, ErrInternal, http.StatusInternalServerError)
				return true
			}

			if selfRespond {
				err := mr.AcceptSelfResponse(s.w)
				if err != nil {
					logs.Out.Error("mmfg AcceptSelfResponse Error", zap.String("node", node), zap.Error(err))
					http.Error(s.w, ErrInternal, http.StatusInternalServerError)
				}
				return true
			}

			continue
		}

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
			handler(s.tempResp, s.r, mr)
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
				mwErr := mw.ForwardMiddleware(s.tempResp, s.r, mr, ext.Path, ext.AllowedHeaders)
				if mwErr == nil {
					approved = true
					break
				}
				if mwErr == forwarder.ErrNodeUnavailable {
					continue
				}
				if errors.Is(mwErr, forwarder.ErrServerError) {
					logs.Out.Error("mmfg Request Header Error", zap.String("expr", mwExpr), zap.Error(mwErr))
					http.Error(s.w, ErrInternal, http.StatusInternalServerError)
					return true
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

	if mr != nil {
		if err := mr.Apply(); err != nil {
			logs.Out.Error("mmfg Apply Error", zap.Error(err))
			http.Error(s.w, ErrInternal, http.StatusInternalServerError)
			return true
		}
	}

	return false
}
