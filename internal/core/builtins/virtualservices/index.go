package virtualservices

import (
	"encoding/json"
	"fmt"
	"nautrouds/internal/core/builtins"
	"nautrouds/internal/core/metrics"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// --- Internal Virtual Services ---

func Echo(args ...string) (http.HandlerFunc, error) {
	if _, err := builtins.CheckArgCount(args, 0, 0); err != nil {
		return nil, fmt.Errorf("$echo: %w", err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		headers := make(map[string]string)
		for k, v := range r.Header {
			headers[k] = strings.Join(v, ", ")
		}

		data := map[string]interface{}{
			"method":      r.Method,
			"path":        r.URL.Path,
			"host":        r.Host,
			"remote_addr": r.RemoteAddr,
			"headers":     headers,
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(data)
	}, nil
}

func OK(args ...string) (http.HandlerFunc, error) {
	if _, err := builtins.CheckArgCount(args, 0, 1); err != nil {
		return nil, fmt.Errorf("$ok: %w", err)
	}
	msg := "OK"
	if len(args) > 0 && args[0] != "" {
		msg = args[0]
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(msg))
	}, nil
}

func ERR(args ...string) (http.HandlerFunc, error) {
	if _, err := builtins.CheckArgCount(args, 0, 2); err != nil {
		return nil, fmt.Errorf("$err: %w", err)
	}
	code := http.StatusBadRequest
	msg := "ERR"

	if len(args) > 0 && args[0] != "" {
		if c, err := strconv.Atoi(args[0]); err == nil {
			code = c
			if len(args) > 1 {
				msg = args[1]
			} else {
				msg = http.StatusText(code)
			}
		} else {
			msg = args[0]
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(msg))
	}, nil
}

func Metrics(args ...string) (http.HandlerFunc, error) {
	if _, err := builtins.CheckArgCount(args, 0, 0); err != nil {
		return nil, fmt.Errorf("$metrics: %w", err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		metrics.Global.WritePrometheus(w, r)
	}, nil
}

// --- Redirect ---

func Redirect(args ...string) (http.HandlerFunc, error) {
	if _, err := builtins.CheckArgCount(args, 2, 2); err != nil {
		return nil, fmt.Errorf("$redirect: %w", err)
	}
	code, _ := strconv.Atoi(args[0])
	target := args[1]
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, code)
	}, nil
}

func Discovery(state map[string][]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(state)
	}
}

func JSON(args ...string) (http.HandlerFunc, error) {
	if _, err := builtins.CheckArgCount(args, 0, 1); err != nil {
		return nil, fmt.Errorf("$json: %w", err)
	}
	data := "{}"
	if len(args) > 0 {
		data = args[0]
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(data))
	}, nil
}

func Ping(targetService string, nodes []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if len(nodes) == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "{\"service\": \"%s\", \"status\": \"unreachable\", \"reason\": \"no nodes discovered\"}\n", targetService)
			return
		}

		// layer 4 connectivity check
		start := time.Now()
		conn, err := net.DialTimeout("unix", nodes[0], 1*time.Second)
		duration := time.Since(start)

		if err != nil {
			w.WriteHeader(http.StatusGatewayTimeout)
			fmt.Fprintf(w, "{\"service\": \"%s\", \"status\": \"down\", \"node\": \"%s\", \"error\": \"%s\"}\n", targetService, nodes[0], err)
			return
		}
		conn.Close()

		fmt.Fprintf(w, "{\"service\": \"%s\", \"status\": \"up\", \"node\": \"%s\", \"latency_ms\": %d}\n", targetService, nodes[0], duration.Milliseconds())
	}
}

// Registry maps virtual service names (with $) to their factories.
var Registry = map[string]builtins.Factory{
	"$echo":     Echo,
	"$ok":       OK,
	"$err":      ERR,
	"$health":   OK,
	"$metrics":  Metrics,
	"$redirect": Redirect,
	"$services": nil, // Runtime resolution
	"$json":     JSON,
	"$ping":     nil, // Runtime resolution
}

// IsValid checks if a virtual service expression is valid.
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
