package test

import (
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/rtree"
	"net/http/httptest"
	"os"
	"testing"
)

func BenchmarkProxy_ServeHTTP(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "nautrouds-bench-*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	reg, err := registry.NewRegistry()
	if err != nil {
		b.Fatalf("failed to create registry: %v", err)
	}

	manager := proxy.NewManager(reg, nil)

	rawNodes := []*rtree.RawNode{
		{
			URL:     "example.com/health",
			Service: "$ok(Healthy)",
			Methods: "GET",
		},
		{
			URL:     "example.com/echo",
			Service: "$echo",
			Methods: "GET",
		},
		{
			URL:     "example.com/api/v1/*",
			Service: "api-service",
			Methods: "GET",
		},
	}
	tree := rtree.Build(rawNodes)
	manager.UpdateGeneration(&proxy.Generation{Tree: *tree})

	b.Run("Virtual Service $ok", func(b *testing.B) {
		req := httptest.NewRequest("GET", "http://example.com/health", nil)
		w := httptest.NewRecorder()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			manager.ServeHTTP(w, req)
		}
	})

	b.Run("Virtual Service $echo", func(b *testing.B) {
		req := httptest.NewRequest("GET", "http://example.com/echo", nil)
		w := httptest.NewRecorder()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			manager.ServeHTTP(w, req)
		}
	})

	b.Run("Routing 404", func(b *testing.B) {
		req := httptest.NewRequest("GET", "http://example.com/not-found", nil)
		w := httptest.NewRecorder()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			manager.ServeHTTP(w, req)
		}
	})
}
