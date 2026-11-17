package rtree_test

import (
	"nautrouds/internal/rtree"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteTree_Search(t *testing.T) {
	rawNodes := []*rtree.RawNode{
		{
			URL:         "example.com/api/v1/users",
			Service:     "user-service",
			Methods:     "GET,POST",
			Middlewares: []string{"auth"},
		},
		{
			URL:     "example.com/api/v1/users/*",
			Service: "user-profile-service",
			Methods: "GET",
		},
		{
			URL:     "*.example.com/static/*",
			Service: "static-service",
			Methods: "GET",
		},
		{
			URL:     "example.com/api/v1/login",
			Service: "auth-service",
			Methods: "POST",
		},
	}

	tree := rtree.Build(rawNodes)
	require.NotNil(t, tree)

	tests := []struct {
		name           string
		url            string
		expectedSvc    string
		expectedExists bool
		checkMethods   uint16
	}{
		{
			name:           "Exact Match - Users",
			url:            "example.com/api/v1/users",
			expectedSvc:    "user-service",
			expectedExists: true,
			checkMethods:   rtree.MethodGet | rtree.MethodPost,
		},
		{
			name:           "Exact Match - Login",
			url:            "example.com/api/v1/login",
			expectedSvc:    "auth-service",
			expectedExists: true,
			checkMethods:   rtree.MethodPost,
		},
		{
			name:           "Wildcard Match - User Profile",
			url:            "example.com/api/v1/users/john",
			expectedSvc:    "user-profile-service",
			expectedExists: true,
			checkMethods:   rtree.MethodGet,
		},
		{
			name:           "Wildcard Host Match - Static",
			url:            "assets.example.com/static/logo.png",
			expectedSvc:    "static-service",
			expectedExists: true,
			checkMethods:   rtree.MethodGet,
		},
		{
			name:           "Wildcard Host Match - Another Static",
			url:            "img.example.com/static/bg.jpg",
			expectedSvc:    "static-service",
			expectedExists: true,
			checkMethods:   rtree.MethodGet,
		},
		{
			name:           "No Match - Wrong Path",
			url:            "example.com/api/v1/unknown",
			expectedExists: false,
		},
		{
			name:           "No Match - Root Only",
			url:            "example.com/",
			expectedExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := []byte(tt.url)
			rtree.ReverseHost(url)
			node, exists := tree.Search(url)

			if !tt.expectedExists {
				assert.False(t, exists, "Expected no match for URL: %s", tt.url)
				assert.Nil(t, node)
				return
			}

			require.True(t, exists, "Expected match for URL: %s", tt.url)

			assert.Equal(t, tt.expectedSvc, getServiceName(tree, node.ActionIndex))
			if tt.checkMethods != 0 {
				assert.Equal(t, tt.checkMethods, node.Methods&tt.checkMethods, "Methods bitmask mismatch")
			}
		})
	}
}

func TestRouteTree_Compression(t *testing.T) {
	rawNodes := []*rtree.RawNode{
		{
			URL:     "nautrouds.io/api/v1",
			Service: "api-svc",
			Methods: http.MethodGet,
		},
	}

	tree := rtree.Build(rawNodes)

	url := []byte("nautrouds.io/api/v1")
	rtree.ReverseHost(url)

	node, exists := tree.Search(url)
	assert.True(t, exists, "Route should be searchable after compression")

	assert.Equal(t, "api-svc", getServiceName(tree, node.ActionIndex))

	rootEdge := tree.EdgePool['i']
	assert.NotZero(t, rootEdge.TargetID, "Root index at 'i' should not be empty")

	fragment := string(tree.FragmentPool[rootEdge.Offset:rootEdge.End])

	expectedFragment := "i"
	assert.Equal(t, expectedFragment, fragment, "The entire unbranched path should be compressed into a single fragment")

	assert.Equal(t, rtree.MethodGet, node.Methods)
}

func TestReverseHost(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"example.com/api", "com.example/api"},
		{"a.b.c/path", "c.b.a/path"},
		{"localhost/v1", "localhost/v1"},
		{"/no-host", "/no-host"},
		{"*/", "*/"},
		{"*/health", "*/health"},
	}

	for _, tt := range tests {
		input := []byte(tt.input)
		rtree.ReverseHost(input)
		assert.Equal(t, tt.expected, string(input), "ReverseHost failed for input: %s", tt.input)
	}
}
