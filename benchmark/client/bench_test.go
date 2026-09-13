package client

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return v
}

var (
	nautroudsSock = getenv("NAUTROUDS_SOCK", "/var/run/bench/nautrouds/entrypoints/nautrouds-0.sock")
	caddySock     = getenv("CADDY_SOCK", "/var/run/bench/caddy/entry.sock")
	nginxSock     = getenv("NGINX_SOCK", "/var/run/bench/nginx/entry.sock")

	caddyTLSNautroudsAddr = getenv("CADDY_TLS_NAUTROUDS_ADDR", "caddy:8443")
	caddyTLSDirectAddr    = getenv("CADDY_TLS_DIRECT_ADDR", "caddy:8444")
	nginxTLSNautroudsAddr = getenv("NGINX_TLS_NAUTROUDS_ADDR", "nginx:8443")
	nginxTLSDirectAddr    = getenv("NGINX_TLS_DIRECT_ADDR", "nginx:8444")

	clientParallelism = getenvInt("CLIENT_PARALLELISM", 64)
)

func newUDSClient(sock string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sock)
			},
			MaxIdleConnsPerHost: clientParallelism,
		},
	}
}

func newTLSClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
			TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
			MaxIdleConnsPerHost: clientParallelism,
		},
	}
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func runBench(b *testing.B, client *http.Client, url string) {
	var mu sync.Mutex
	var allLatencies []time.Duration

	b.SetParallelism(clientParallelism)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		local := make([]time.Duration, 0, 1024)
		for pb.Next() {
			start := time.Now()
			resp, err := client.Get(url)
			elapsed := time.Since(start)
			if err != nil {
				b.Fatalf("request failed: %v", err)
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				resp.Body.Close()
				b.Fatalf("non-2xx response: %d", resp.StatusCode)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			local = append(local, elapsed)
		}
		mu.Lock()
		allLatencies = append(allLatencies, local...)
		mu.Unlock()
	})
	b.StopTimer()

	slices.Sort(allLatencies)
	p50 := percentile(allLatencies, 0.50)
	p99 := percentile(allLatencies, 0.99)
	b.ReportMetric(float64(p50.Microseconds())/1000.0, "p50-ms")
	b.ReportMetric(float64(p99.Microseconds())/1000.0, "p99-ms")
}

func BenchmarkNautroudsBuiltin(b *testing.B) {
	runBench(b, newUDSClient(nautroudsSock), "http://localhost/builtin/health")
}

func BenchmarkNautroudsProxy(b *testing.B) {
	runBench(b, newUDSClient(nautroudsSock), "http://localhost/proxy/bench")
}

func BenchmarkCaddyBuiltin(b *testing.B) {
	runBench(b, newUDSClient(caddySock), "http://localhost/builtin/health")
}

func BenchmarkCaddyProxy(b *testing.B) {
	runBench(b, newUDSClient(caddySock), "http://localhost/proxy/bench")
}

func BenchmarkNginxBuiltin(b *testing.B) {
	runBench(b, newUDSClient(nginxSock), "http://localhost/builtin/health")
}

func BenchmarkNginxProxy(b *testing.B) {
	runBench(b, newUDSClient(nginxSock), "http://localhost/proxy/bench")
}

func BenchmarkCaddyTLSNautroudsProxy(b *testing.B) {
	runBench(b, newTLSClient(), "https://"+caddyTLSNautroudsAddr+"/proxy/bench")
}

func BenchmarkCaddyTLSDirectProxy(b *testing.B) {
	runBench(b, newTLSClient(), "https://"+caddyTLSDirectAddr+"/proxy/bench")
}

func BenchmarkNginxTLSNautroudsProxy(b *testing.B) {
	runBench(b, newTLSClient(), "https://"+nginxTLSNautroudsAddr+"/proxy/bench")
}

func BenchmarkNginxTLSDirectProxy(b *testing.B) {
	runBench(b, newTLSClient(), "https://"+nginxTLSDirectAddr+"/proxy/bench")
}
