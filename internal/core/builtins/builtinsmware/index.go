package builtinsmware

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"nautrouds/internal/core/logs"
	"nautrouds/internal/core/mmfg"
	"nautrouds/internal/core/tempresp"
	"net"
	"net/http"
	"net/url"
	"strings"

	"go.uber.org/zap"
)

type MiddlewareFactory func(args ...string) HandlerFunc
type HandlerFunc = func(*tempresp.ResponseWriter, *http.Request, mmfg.Request)

func InvalidMiddleware(w *tempresp.ResponseWriter, r *http.Request, mr mmfg.Request) {
	w.Reply("Internal Server Error", http.StatusInternalServerError)
}

// --- Header Operations ---

func SetHeader(args ...string) HandlerFunc {
	key, val := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request, mr mmfg.Request) {
		if mr != nil {
			if err := mr.UpdateHeader(key, val); err != nil {
				replyMmfgError(w, "Failed to update mmfg request header", err)
			}
		} else {
			r.Header.Set(key, val)
		}
	}
}

func DelHeader(args ...string) HandlerFunc {
	key, _ := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request, mr mmfg.Request) {
		if mr != nil {
			if err := mr.DeleteHeader(key); err != nil {
				replyMmfgError(w, "Failed to delete mmfg request header", err)
			}
		} else {
			r.Header.Del(key)
		}
	}
}

func SetHost(args ...string) HandlerFunc {
	host, _ := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request, mr mmfg.Request) {
		r.Host = host
	}
}

// --- Path & Query Operations ---

func getURL(r *http.Request, mr mmfg.Request) (*url.URL, error) {
	if mr != nil {
		return mr.URL()
	}
	return r.URL, nil
}

func replyMmfgError(w *tempresp.ResponseWriter, msg string, err error) {
	logs.Out.Error(msg, zap.Error(err))
	w.Reply("Internal Server Error", http.StatusInternalServerError)
}

func applyURL(u *url.URL, r *http.Request, mr mmfg.Request) error {
	raw := u.RequestURI()
	if mr != nil {
		return mr.SetURL(raw)
	}
	r.RequestURI = raw
	return nil
}

func PathTrimPrefix(args ...string) HandlerFunc {
	prefix, _ := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request, mr mmfg.Request) {
		u, err := getURL(r, mr)
		if err != nil {
			replyMmfgError(w, "Failed to get request URL", err)
			return
		}

		if after, ok := strings.CutPrefix(u.Path, prefix); ok {
			u.Path = after

			if u.RawPath != "" {
				u.RawPath = strings.TrimPrefix(u.RawPath, url.PathEscape(prefix))
			}

			if err := applyURL(u, r, mr); err != nil {
				replyMmfgError(w, "Failed to write request URL", err)
				return
			}
		}
	}
}

func RewritePath(args ...string) HandlerFunc {
	old, new := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request, mr mmfg.Request) {
		u, err := getURL(r, mr)
		if err != nil {
			replyMmfgError(w, "Failed to get request URL", err)
			return
		}

		u.Path = strings.ReplaceAll(u.Path, old, new)

		if u.RawPath != "" {
			u.RawPath = strings.ReplaceAll(u.RawPath, old, new)
		}

		if err := applyURL(u, r, mr); err != nil {
			replyMmfgError(w, "Failed to write request URL", err)
			return
		}
	}
}

func SetQuery(args ...string) HandlerFunc {
	key, val := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request, mr mmfg.Request) {
		u, err := getURL(r, mr)
		if err != nil {
			replyMmfgError(w, "Failed to get request URL", err)
			return
		}
		q := u.Query()
		q.Set(key, val)
		u.RawQuery = q.Encode()
		if err := applyURL(u, r, mr); err != nil {
			replyMmfgError(w, "Failed to write request URL", err)
			return
		}
	}
}

// --- Security & Auth ---

func parseBasicAuth(auth string) (username, password string, ok bool) {
	const prefix = "Basic "
	if len(auth) < len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(auth[len(prefix):])
	if err != nil {
		return "", "", false
	}
	username, password, ok = strings.Cut(string(decoded), ":")
	if !ok {
		return "", "", false
	}
	return username, password, true
}

func BasicAuth(args ...string) HandlerFunc {
	user, pass := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request, mr mmfg.Request) {
		var u, p string
		var ok bool

		if mr != nil {
			auth, err := mr.Header("Authorization")
			if err != nil {
				replyMmfgError(w, "Failed to read mmfg request header", err)
				return
			}
			u, p, ok = parseBasicAuth(auth)
		} else {
			u, p, ok = r.BasicAuth()
		}

		if !ok || u != user || p != pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="Nautrouds Protected"`)
			w.WriteHeader(http.StatusUnauthorized)
		}
	}
}

func RequireHeader(args ...string) HandlerFunc {
	key, val := parseTwoArgs(args)
	return func(w *tempresp.ResponseWriter, r *http.Request, mr mmfg.Request) {
		var got string
		if mr != nil {
			h, err := mr.Header(key)
			if err != nil {
				replyMmfgError(w, "Failed to read mmfg request header", err)
				return
			}
			got = h
		} else {
			got = r.Header.Get(key)
		}

		if subtle.ConstantTimeCompare([]byte(got), []byte(val)) != 1 {
			w.Reply("Forbidden", http.StatusForbidden)
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
	return func(w *tempresp.ResponseWriter, r *http.Request, mr mmfg.Request) {
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
	return func(w *tempresp.ResponseWriter, r *http.Request, mr mmfg.Request) {
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
	"$RequireHeader":  RequireHeader,
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
