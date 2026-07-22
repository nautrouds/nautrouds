package compiler_test

import (
	"fmt"
	"nautrouds/internal/compiler"
	"nautrouds/internal/rtree"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	script := `
# This is a comment
* example.com/api/v1 svc-1
    mw-auth
    mw-log

GET example.com/api/v2 svc-2
    # Nested comment
    mw-cache

# Case with omitted Method (defaults to *)
example.com/api/v3 svc-3
`

	tree, err := compiler.ParseString(script)
	require.NoError(t, err)
	require.NotNil(t, tree)

	t.Run("Middleware Inheritance", func(t *testing.T) {
		url := []byte("example.com/api/v1")
		rtree.ReverseHost(url)
		node, exists := tree.Search(url)
		require.True(t, exists)

		serviceIndex := tree.ActionMetadata[node.ActionIndex]
		serviceID := tree.ActionMetadata[serviceIndex]
		assert.Equal(t, "svc-1", tree.GetActionName(serviceID))

		mwCount := tree.ActionMetadata[node.ActionIndex+1]
		// Verify middlewares are correctly compiled
		var mws []string
		for i := range mwCount {
			mwMetaIndex := tree.ActionMetadata[node.ActionIndex+2+i]
			mws = append(mws, tree.GetActionName(tree.ActionMetadata[mwMetaIndex]))
		}
		assert.ElementsMatch(t, []string{"mw-auth", "mw-log"}, mws)
	})

	t.Run("Method Filtering", func(t *testing.T) {
		url := []byte("example.com/api/v2")
		rtree.ReverseHost(url)
		node, exists := tree.Search(url)
		require.True(t, exists)
		assert.Equal(t, rtree.MethodGet, node.Methods&rtree.MethodGet)
	})

	t.Run("Default Method Star", func(t *testing.T) {
		url := []byte("example.com/api/v3")
		rtree.ReverseHost(url)
		node, exists := tree.Search(url)
		require.True(t, exists)
		assert.Equal(t, rtree.MethodAny, node.Methods)
	})
}

func TestParse_WithExpansion(t *testing.T) {
	script := `
GET [a|b].io/api svc-expanded
`
	tree, err := compiler.ParseString(script)
	require.NoError(t, err)

	// Verify both expanded paths point to the same service
	urls := []string{"a.io/api", "b.io/api"}
	for _, u := range urls {
		url := []byte(u)
		rtree.ReverseHost(url)
		node, exists := tree.Search(url)
		assert.True(t, exists, "Path %s should exist", u)
		serviceIndex := tree.ActionMetadata[node.ActionIndex]
		serviceID := tree.ActionMetadata[serviceIndex]
		assert.Equal(t, "svc-expanded", tree.GetActionName(serviceID))
	}
}

func TestParse_InvalidBuiltin(t *testing.T) {
	script := `
GET example.com/api svc
    $NonExistentFunc()
`
	_, err := compiler.ParseString(script)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown builtin middleware")
}

func TestParse_ValidMmfg(t *testing.T) {
	script := `
GET example.com/api svc
    $mmfg(mmfg-service/echo)
`
	tree, err := compiler.ParseString(script)
	require.NoError(t, err)
	require.NotNil(t, tree)

	url := []byte("example.com/api")
	rtree.ReverseHost(url)
	node, exists := tree.Search(url)
	require.True(t, exists)

	mwCount := tree.ActionMetadata[node.ActionIndex+1]
	var mws []string
	for i := range mwCount {
		mwMetaIndex := tree.ActionMetadata[node.ActionIndex+2+i]
		mws = append(mws, tree.GetActionName(tree.ActionMetadata[mwMetaIndex]))
	}
	assert.ElementsMatch(t, []string{"$mmfg(mmfg-service/echo)"}, mws)
}

func TestParse_InvalidMmfg(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{"MissingClosingParen", "$mmfg("},
		{"EmptyNodeName", "$mmfg()"},
		{"UnterminatedNodeName", "$mmfg(foo"},
		{"NoParens", "$mmfg"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			script := fmt.Sprintf(`
GET example.com/api svc
    %s
`, c.expr)
			_, err := compiler.ParseString(script)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "invalid $mmfg")
		})
	}
}
