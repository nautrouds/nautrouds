package main

import (
	"fmt"
	"strconv"
	"strings"
)

type TargetKind int

const (
	KindBuiltin TargetKind = iota
	KindProxy
)

type Target struct {
	Name     string
	Proxy    string
	Category string
	Address  string
	BasePath string
	Kind     TargetKind
}

type Stage struct {
	Profile         string
	Targets         []Target
	ProxyServices   []string
	BackendServices []string
	SweepTiers      bool
}

const (
	nautroudsEntrySock = "/var/run/nautrouds/entrypoints/nautrouds-0.sock"
	nautroudsBackend   = "/var/run/nautrouds/services/backend-service/node-0.sock"
	caddyEntrySock     = "/var/run/bench/caddy/entry.sock"
	nginxEntrySock     = "/var/run/bench/nginx/entry.sock"
)

func builtinTarget(proxy, sock string) Target {
	return Target{Name: proxy + "Builtin", Proxy: proxy, Category: "uds_builtin", Address: "unix://" + sock, BasePath: "/builtin/health", Kind: KindBuiltin}
}

func proxyTarget(name, proxy, category, address string) Target {
	return Target{Name: name, Proxy: proxy, Category: category, Address: address, BasePath: "/proxy/bench", Kind: KindProxy}
}

var stages = []Stage{
	{
		Profile:         "baseline",
		Targets:         []Target{proxyTarget("Direct", "Direct", "direct", "unix://"+nautroudsBackend)},
		BackendServices: []string{"backend-nautrouds"},
	},
	{
		Profile:       "nautrouds-solo",
		Targets:       []Target{builtinTarget("Nautrouds", nautroudsEntrySock)},
		ProxyServices: []string{"nautrouds"},
		SweepTiers:    true,
	},
	{
		Profile:         "nautrouds-proxy",
		Targets:         []Target{proxyTarget("NautroudsProxy", "Nautrouds", "uds_proxy", "unix://"+nautroudsEntrySock)},
		ProxyServices:   []string{"nautrouds"},
		BackendServices: []string{"backend-nautrouds"},
		SweepTiers:      true,
	},
	{
		Profile:       "caddy-solo",
		Targets:       []Target{builtinTarget("Caddy", caddyEntrySock)},
		ProxyServices: []string{"caddy"},
		SweepTiers:    true,
	},
	{
		Profile: "caddy-proxy",
		Targets: []Target{
			proxyTarget("CaddyProxy", "Caddy", "uds_proxy", "unix://"+caddyEntrySock),
			proxyTarget("CaddyTLSDirectProxy", "Caddy", "tls_direct", "tls://caddy:8444"),
		},
		ProxyServices:   []string{"caddy"},
		BackendServices: []string{"backend-caddy"},
		SweepTiers:      true,
	},
	{
		Profile:       "nginx-solo",
		Targets:       []Target{builtinTarget("Nginx", nginxEntrySock)},
		ProxyServices: []string{"nginx"},
		SweepTiers:    true,
	},
	{
		Profile: "nginx-proxy",
		Targets: []Target{
			proxyTarget("NginxProxy", "Nginx", "uds_proxy", "unix://"+nginxEntrySock),
			proxyTarget("NginxTLSDirectProxy", "Nginx", "tls_direct", "tls://nginx:8444"),
		},
		ProxyServices:   []string{"nginx"},
		BackendServices: []string{"backend-nginx"},
		SweepTiers:      true,
	},
	{
		Profile:         "caddy-tls",
		Targets:         []Target{proxyTarget("CaddyTLSNautroudsProxy", "Caddy", "tls_via_nautrouds", "tls://caddy:8443")},
		ProxyServices:   []string{"caddy", "nautrouds"},
		BackendServices: []string{"backend-nautrouds"},
		SweepTiers:      true,
	},
	{
		Profile:         "nginx-tls",
		Targets:         []Target{proxyTarget("NginxTLSNautroudsProxy", "Nginx", "tls_via_nautrouds", "tls://nginx:8443")},
		ProxyServices:   []string{"nginx", "nautrouds"},
		BackendServices: []string{"backend-nautrouds"},
		SweepTiers:      true,
	},
}

var buildableServices = []string{"nautrouds", "backend-nautrouds", "backend-caddy", "backend-nginx", "client"}

const backendDefaultBodySize = 64

type Workload struct {
	Name           string
	Title          string
	Conns          int
	Rate           float64
	CloseEach      bool
	Query          string
	Method         string
	RequestBody    int
	ExpectBodySize int
	ProxyOnly      bool
}

func (w Workload) path(t Target) string {
	if w.Query == "" || t.Kind != KindProxy {
		return t.BasePath
	}
	return t.BasePath + "?" + w.Query
}

func (w Workload) expectedBodySize(t Target) int {
	if t.Kind != KindProxy {
		return -1
	}
	if w.ExpectBodySize > 0 {
		return w.ExpectBodySize
	}
	return backendDefaultBodySize
}

func (w Workload) appliesTo(t Target) bool {
	return !w.ProxyOnly || t.Kind == KindProxy
}

func buildWorkloads(rates []float64) []Workload {
	workloads := []Workload{
		{Name: "latency-c1", Title: "Unloaded latency (1 connection, closed-loop)", Conns: 1},
		{Name: "throughput-c64", Title: "Max throughput (64 connections, closed-loop)", Conns: 64},
		{Name: "throughput-c512", Title: "High concurrency (512 connections, closed-loop)", Conns: 512},
	}
	for _, rate := range rates {
		workloads = append(workloads, Workload{
			Name:  fmt.Sprintf("rate-%d", int(rate)),
			Title: fmt.Sprintf("Latency at a fixed %d req/s (64 connections, open-loop)", int(rate)),
			Conns: 64,
			Rate:  rate,
		})
	}
	return append(workloads,
		Workload{Name: "conn-per-request", Title: "New connection per request (64 connections, Connection: close)", Conns: 64, CloseEach: true},
		Workload{Name: "body-64k", Title: "64 KiB response body (64 connections)", Conns: 64, Query: "size=65536", ExpectBodySize: 65536, ProxyOnly: true},
		Workload{Name: "body-1m", Title: "1 MiB response body (16 connections)", Conns: 16, Query: "size=1048576", ExpectBodySize: 1048576, ProxyOnly: true},
		Workload{Name: "upload-64k", Title: "64 KiB request body, POST (64 connections)", Conns: 64, Method: "POST", RequestBody: 65536, ProxyOnly: true},
		Workload{Name: "slow-backend-10ms", Title: "Backend responds after 10 ms (256 connections)", Conns: 256, Query: "delay=10ms", ProxyOnly: true},
	)
}

func parseRates(value string) ([]float64, error) {
	var rates []float64
	for _, field := range strings.Split(value, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		rate, err := strconv.ParseFloat(field, 64)
		if err != nil || rate <= 0 {
			return nil, fmt.Errorf("invalid rate %q", field)
		}
		rates = append(rates, rate)
	}
	return rates, nil
}

func filterByName[T any](items []T, filter string, name func(T) string) []T {
	if filter == "" {
		return items
	}
	want := make(map[string]bool)
	for _, n := range strings.Split(filter, ",") {
		want[strings.TrimSpace(n)] = true
	}
	var selected []T
	for _, item := range items {
		if want[name(item)] {
			selected = append(selected, item)
		}
	}
	return selected
}
