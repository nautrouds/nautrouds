package test

import (
	"nautrouds/internal/core/proxy"
	"nautrouds/internal/core/registry"
	"nautrouds/internal/rtree"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func BenchmarkProxy_ServeHTTP(b *testing.B) {
	reg, err := registry.NewRegistry()
	require.NoError(b, err)

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

	benchmarks := []struct {
		name string
		url  string
	}{
		{"Virtual Service $ok", "http://example.com/health"},
		{"Virtual Service $echo", "http://example.com/echo"},
		{"Routing 404", "http://example.com/not-found"},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			req := httptest.NewRequest("GET", bm.url, nil)
			w := httptest.NewRecorder()
			for b.Loop() {
				manager.ServeHTTP(w, req)
			}
		})
	}
}
