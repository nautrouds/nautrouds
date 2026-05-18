package rtree_test

import (
	"fmt"
	"nautilus/internal/rtree"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareURL(url string) []byte {
	return rtree.ReverseHost([]byte(url))
}

func getServiceName(tree *rtree.RouteTree, actionIndex uint32) string {
	actionID := tree.ActionMetadata[actionIndex]
	registryIndex := tree.ActionMetadata[actionID]
	return tree.GetActionName(registryIndex)
}

func TestStressExtremeWildcards(t *testing.T) {
	rawNodes := []*rtree.RawNode{}

	// Generate 50+ nodes with complex patterns
	for i := range 50 {
		// Mix static, wildcard (*), and greedy wildcard (**)
		path := fmt.Sprintf("api/v%d/*/*/resource_%d", i%5, i)
		if i%10 == 0 {
			path = fmt.Sprintf("api/v%d/**/resource_%d", i%5, i)
		} else if i%3 == 0 {
			path = fmt.Sprintf("api/v%d/*/sub/*", i%5)
		}

		rawNodes = append(rawNodes, &rtree.RawNode{
			URL:     fmt.Sprintf("domain%d.com/%s", i%5, path),
			Service: fmt.Sprintf("service-%d", i),
			Methods: "GET",
		})
	}

	// Add extreme overlaps
	rawNodes = append(rawNodes, &rtree.RawNode{URL: "domain0.com/api/v0/**", Service: "greedy-global", Methods: "GET"})
	rawNodes = append(rawNodes, &rtree.RawNode{URL: "domain0.com/api/v0/*/*/resource_0", Service: "specific-match", Methods: "GET"})

	tree := rtree.Build(rawNodes)
	require.NotNil(t, tree)

	// Test a specific match
	url := prepareURL("domain0.com/api/v0/a/b/resource_0")
	node, exists := tree.Search(url)
	assert.True(t, exists)
	assert.Equal(t, "specific-match", getServiceName(tree, node.ActionIndex))

	// Test greedy fallback
	url2 := prepareURL("domain0.com/api/v0/some/deep/path/that/is/not/specific")
	node2, exists2 := tree.Search(url2)
	assert.True(t, exists2)
	assert.Equal(t, "greedy-global", getServiceName(tree, node2.ActionIndex))
}

func TestMultiLevelWildcardAndBacktracking(t *testing.T) {
	rawNodes := []*rtree.RawNode{
		{URL: "*.com/api/*/*/details", Service: "wildcard-details-service", Methods: "GET"},
		{URL: "*.com/api/*/**", Service: "greedy-fallback-service", Methods: "GET"},
	}

	tree := rtree.Build(rawNodes)
	require.NotNil(t, tree)

	urlA := prepareURL("example.com/api/v1/v2/details")
	nodeA, existsA := tree.Search(urlA)
	assert.True(t, existsA)
	if existsA {
		assert.Equal(t, "wildcard-details-service", getServiceName(tree, nodeA.ActionIndex))
	}

	urlB := prepareURL("example.com/api/v1/v2/corrupted")
	nodeB, existsB := tree.Search(urlB)
	assert.True(t, existsB)
	if existsB {
		assert.Equal(t, "greedy-fallback-service", getServiceName(tree, nodeB.ActionIndex))
	}
}

func TestSharedPrefixGraftIsolation(t *testing.T) {
	rawNodes := []*rtree.RawNode{
		{URL: "*.com/*/assets/images/logo.png", Service: "user-logo-service", Methods: "GET"},
		{URL: "*.com/static/assets/images/logo.png", Service: "static-logo-service", Methods: "GET"},
	}

	tree := rtree.Build(rawNodes)
	require.NotNil(t, tree)

	urlA := prepareURL("example.com/static/assets/images/wrong.png")
	_, existsA := tree.Search(urlA)
	assert.False(t, existsA)

	urlB := prepareURL("example.com/alex/assets/images/logo.png")
	nodeB, existsB := tree.Search(urlB)
	assert.True(t, existsB)
	if existsB {
		assert.Equal(t, "user-logo-service", getServiceName(tree, nodeB.ActionIndex))
	}
}

func TestHostReversalAndVariableLengths(t *testing.T) {
	rawNodes := []*rtree.RawNode{{URL: "*/api/v1/status", Service: "global-status-service", Methods: "GET"}}
	tree := rtree.Build(rawNodes)
	require.NotNil(t, tree)

	urlLong := prepareURL("sub.deep.internal.enterprise.co.uk/api/v1/status")
	nodeL, existsL := tree.Search(urlLong)
	assert.True(t, existsL)
	assert.Equal(t, "global-status-service", getServiceName(tree, nodeL.ActionIndex))
}
