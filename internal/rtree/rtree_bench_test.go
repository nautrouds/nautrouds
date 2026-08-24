package rtree

import (
	"testing"
)

func BenchmarkSearch(b *testing.B) {
	nodes := []*RawNode{
		{URL: "/users/*", Methods: "GET", Service: "UserService"},
		{URL: "/users", Methods: "GET", Service: "UserService"},
		{URL: "/posts/*/comments", Methods: "GET", Service: "PostService"},
		{URL: "/api/v1/health", Methods: "GET", Service: "HealthService"},
		{URL: "/static/*", Methods: "GET", Service: "FileService"},
	}
	tree := Build(nodes)
	if tree == nil {
		b.Fatal("Failed to build tree")
	}

	searchURLs := [][]byte{
		[]byte("/users/123"),
		[]byte("/users"),
		[]byte("/posts/456/comments"),
		[]byte("/api/v1/health"),
		[]byte("/static/css/main.css"),
	}

	for b.Loop() {
		for _, url := range searchURLs {
			_, found := tree.Search(url)
			if !found {
				b.Errorf("URL %s not found", url)
			}
		}
	}
}
