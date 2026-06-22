package builtinsmware

import (
	"fmt"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/tempresp"
	"net"
	"net/http"
	"net/url"
	"strings"

	"go.uber.org/zap"
)

type MiddlewareFactory func(args ...string) HandlerFunc
type HandlerFunc = func(*tempresp.ResponseWriter, *http.Request)

func InvalidMiddleware(w *tempresp.ResponseWriter, r *http.Request) {
	w.Reply("Internal Server Error", http.StatusInternalServerError)
}

// --- Header Operations ---

func SetHeader(args ...string) HandlerFunc {
	key, val := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request) {
		r.Header.Set(key, val)
	}
}

func DelHeader(args ...string) HandlerFunc {
	key, _ := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request) {
		r.Header.Del(key)
	}
}

func SetHost(args ...string) HandlerFunc {
	host, _ := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request) {
		r.Host = host
	}
}

// --- Path & Query Operations ---

func PathTrimPrefix(args ...string) HandlerFunc {
	prefix, _ := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request) {

		if after, ok := strings.CutPrefix(r.URL.Path, prefix); ok {
			r.URL.Path = after

			if r.URL.RawPath != "" {
				r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, url.PathEscape(prefix))
			}
			r.RequestURI = r.URL.RequestURI()
		}
	}
}

func RewritePath(args ...string) HandlerFunc {
	old, new := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.ReplaceAll(r.URL.Path, old, new)

		if r.URL.RawPath != "" {
			r.URL.RawPath = strings.ReplaceAll(r.URL.RawPath, old, new)
		}
		r.RequestURI = r.URL.RequestURI()
	}
}

func SetQuery(args ...string) HandlerFunc {
	key, val := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		q.Set(key, val)
		r.URL.RawQuery = q.Encode()
		r.RequestURI = r.URL.RequestURI()
	}
}

// --- Security & Auth ---

func BasicAuth(args ...string) HandlerFunc {
	user, pass := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="Nautrouds Protected"`)
			w.WriteHeader(http.StatusUnauthorized)
		}
	}
}

func IPAllow(args ...string) HandlerFunc {
	if len(args) != 1 {
		logs.Out.Error("IPAllow error: expected 1 argument")
		return InvalidMiddleware
	}
	_, ipNet, err := net.ParseCIDR(args[0])
	if err != nil {
		logs.Out.Error("IPAllow error: invalid CIDR", zap.Error(err))
		return InvalidMiddleware
	}
	return func(w *tempresp.ResponseWriter, r *http.Request) {
		ipStr, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(ipStr)
		if !ipNet.Contains(ip) {
			w.Reply("Forbidden", http.StatusForbidden)
		}
	}
}

// --- Debugging & Utilities ---

func Log(args ...string) HandlerFunc {
	prefix, _ := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request) {
		fmt.Printf("[%s] %s %s from %s\n", prefix, r.Method, r.URL.Path, r.RemoteAddr)
	}
}

// --- Registry ---

var Registry = map[string]MiddlewareFactory{
	"$SetHeader":      SetHeader,
	"$DelHeader":      DelHeader,
	"$SetHost":        SetHost,
	"$PathTrimPrefix": PathTrimPrefix,
	"$RewritePath":    RewritePath,
	"$SetQuery":       SetQuery,
	"$BasicAuth":      BasicAuth,
	"$IPAllow":        IPAllow,
	"$Log":            Log,
}

// IsValid checks if an expression looks like a builtin and if it exists in the registry.
func IsValid(expr string) (bool, string) {
	if !strings.HasPrefix(expr, "$") {
		return false, ""
	}

	funcName := expr
	start := strings.Index(expr, "(")
	if start != -1 {
		funcName = expr[:start]
	}

	_, ok := Registry[funcName]
	if !ok {
		return false, funcName
	}

	if start != -1 {
		end := strings.LastIndex(expr, ")")
		if end == -1 || end < start {
			return false, ""
		}
	}

	return true, ""
}

func parseTwoArgs(args []string) (string, string) {
	switch len(args) {
	case 1:
		return args[0], ""
	case 2:
		return args[0], args[1]
	default:
		return "", ""
	}
}
