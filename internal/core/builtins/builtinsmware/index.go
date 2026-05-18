package builtinsmware

import (
	"bytes"
	"fmt"
	"io"
	"nautrouds/internal/core/logs"
	"net"
	"net/http"
	"net/url"
	"strings"

	"go.uber.org/zap"
)

type MiddlewareFactory func(args ...string) HandlerFunc
type HandlerFunc = func(*ResponseWriter, *http.Request)

type ResponseWriter struct {
	header     http.Header
	statusCode int
	body       *bytes.Buffer
}

func NewResponseWriter() *ResponseWriter {
	return &ResponseWriter{
		header:     make(http.Header),
		body:       new(bytes.Buffer),
		statusCode: http.StatusOK,
	}
}

func (m *ResponseWriter) Header() http.Header {
	return m.header
}

func (m *ResponseWriter) GetCode() int {
	return m.statusCode
}

func (m *ResponseWriter) WriteTo(w http.ResponseWriter) error {
	for key, values := range m.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(m.statusCode)
	_, err := m.body.WriteTo(w)
	return err
}

func (m *ResponseWriter) Reply(msg string, code int) {
	m.body.WriteString(msg)
	m.statusCode = code
}

func (m *ResponseWriter) ReplyReader(rc io.ReadCloser, code int) error {
	defer rc.Close()
	m.statusCode = code
	_, err := io.Copy(m.body, rc)
	if err != nil {
		return err
	}

	return nil
}

func InvalidMiddleware(w *ResponseWriter, r *http.Request) {
	w.body.WriteString("Invalid Middleware")
	w.statusCode = http.StatusBadRequest
}

// --- Header Operations ---

func SetHeader(args ...string) HandlerFunc {
	key, val := parseTwoArgs(args)
	return func(w *ResponseWriter, r *http.Request) {
		r.Header.Set(key, val)
	}
}

func DelHeader(args ...string) HandlerFunc {
	key, _ := parseTwoArgs(args)
	return func(w *ResponseWriter, r *http.Request) {
		r.Header.Del(key)
	}
}

func SetHost(args ...string) HandlerFunc {
	host, _ := parseTwoArgs(args)
	return func(w *ResponseWriter, r *http.Request) {
		r.Host = host
	}
}

// --- Path & Query Operations ---

func PathTrimPrefix(args ...string) HandlerFunc {
	prefix, _ := parseTwoArgs(args)
	return func(w *ResponseWriter, r *http.Request) {

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
	return func(w *ResponseWriter, r *http.Request) {
		r.URL.Path = strings.ReplaceAll(r.URL.Path, old, new)

		if r.URL.RawPath != "" {
			r.URL.RawPath = strings.ReplaceAll(r.URL.RawPath, old, new)
		}
		r.RequestURI = r.URL.RequestURI()
	}
}

func SetQuery(args ...string) HandlerFunc {
	key, val := parseTwoArgs(args)
	return func(w *ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		q.Set(key, val)
		r.URL.RawQuery = q.Encode()
		r.RequestURI = r.URL.RequestURI()
	}
}

// --- Security & Auth ---

func BasicAuth(args ...string) HandlerFunc {
	user, pass := parseTwoArgs(args)
	return func(w *ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pass {
			w.statusCode = http.StatusUnauthorized
			w.header.Set("WWW-Authenticate", `Basic realm="Nautrouds Protected"`)
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
	return func(w *ResponseWriter, r *http.Request) {
		ipStr, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(ipStr)
		if !ipNet.Contains(ip) {
			w.statusCode = http.StatusForbidden
			w.body.WriteString("Forbidden: IP not allowed")
		}
	}
}

// --- Debugging & Utilities ---

func Log(args ...string) HandlerFunc {
	prefix, _ := parseTwoArgs(args)
	return func(w *ResponseWriter, r *http.Request) {
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
