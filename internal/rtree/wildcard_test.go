package rtree_test

import (
	"bytes"
	"fmt"
	"log"
	"nautrouds/internal/rtree"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareURL(url string) []byte {
	urlBytes := []byte(url)
	rtree.ReverseHost(urlBytes)
	return urlBytes
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

func TestStaticLeafFallsBackToWildcard(t *testing.T) {
	cases := []struct {
		routes []string
		url    string
		want   string
	}{
		{[]string{"/api/*", "/api/health"}, "/api/health", "/api/health"},
		{[]string{"/api/*", "/api/health"}, "/api/healthz", "/api/*"},
		{[]string{"/api/*", "/api/health"}, "/api/health/x", "/api/*"},
		{[]string{"/x/**", "/x/ab"}, "/x/abc", "/x/**"},
		{[]string{"/x/**", "/x/ab"}, "/x/ab/c", "/x/**"},
		{[]string{"/user/*", "/user/me"}, "/user/meow", "/user/*"},
		{[]string{"/x/*/**", "/x/ab"}, "/x/ab/zz", "/x/*/**"},
		{[]string{"/a/**x/y", "/a/x"}, "/a/x/y", "/a/**x/y"},
		{[]string{"/a/**x/y", "/a/x", "/a/x/z"}, "/a/x/y", "/a/**x/y"},
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("%v %s", c.routes, c.url), func(t *testing.T) {
			rawNodes := make([]*rtree.RawNode, 0, len(c.routes))
			for _, route := range c.routes {
				rawNodes = append(rawNodes, &rtree.RawNode{URL: "example.com" + route, Service: route, Methods: "GET"})
			}

			tree := rtree.Build(rawNodes)
			require.NotNil(t, tree)

			node, exists := tree.Search(prepareURL("example.com" + c.url))
			require.True(t, exists)
			assert.Equal(t, c.want, getServiceName(tree, node.ActionIndex))
		})
	}
}

func TestWildcardLeafMatchPriority(t *testing.T) {
	cases := []struct {
		routes []string
		url    string
		want   string
	}{
		{[]string{"/a/b**", "/a/*"}, "/a/b", "/a/*"},
		{[]string{"/a/b*", "/a/*"}, "/a/b", "/a/*"},
		{[]string{"/a/b*", "/a/**"}, "/a/b", "/a/**"},
		{[]string{"/a/b*", "/a/*"}, "/a/bz", "/a/b*"},
		{[]string{"/a/*", "/a/**"}, "/a/x", "/a/*"},
		{[]string{"/a/**.json*", "/a/**"}, "/a/x.json", "/a/**"},
		{[]string{"/a/**.json*", "/a/**"}, "/a/x.json/y.json", "/a/**.json*"},
		{[]string{"/a/**.json*", "/a/**"}, "/a/x.jsonp", "/a/**.json*"},
		{[]string{"/a/**.json*", "/a/**"}, "/a/x/y.json", "/a/**"},
		{[]string{"/a/**", "/a/**/x/y", "/a/**/*"}, "/a/q/x/y", "/a/**/x/y"},
		{[]string{"/a/**", "/a/**/x/y", "/a/**/*"}, "/a/q/x/z", "/a/**/*"},
		{[]string{"/a/**", "/a/**/x/y", "/a/**/*"}, "/a/q/b", "/a/**/*"},
		{[]string{"/a/**", "/a/**/x/y"}, "/a/q/x/z", "/a/**"},
		{[]string{"/a/**", "/a/**/x/y"}, "/a/q", "/a/**"},
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("%v %s", c.routes, c.url), func(t *testing.T) {
			rawNodes := make([]*rtree.RawNode, 0, len(c.routes))
			for _, route := range c.routes {
				rawNodes = append(rawNodes, &rtree.RawNode{URL: "example.com" + route, Service: route, Methods: "GET"})
			}

			tree := rtree.Build(rawNodes)
			require.NotNil(t, tree)

			node, exists := tree.Search(prepareURL("example.com" + c.url))
			require.True(t, exists)
			assert.Equal(t, c.want, getServiceName(tree, node.ActionIndex))
		})
	}
}

func TestWildcardLimitPerRoute(t *testing.T) {
	atLimit := "example.com" + strings.Repeat("/*", rtree.MaxWildcardsPerRoute)
	tree := rtree.Build([]*rtree.RawNode{{URL: atLimit, Service: "at-limit", Methods: "GET"}})
	require.NotNil(t, tree)

	node, exists := tree.Search(prepareURL("example.com" + strings.Repeat("/x", rtree.MaxWildcardsPerRoute)))
	require.True(t, exists)
	assert.Equal(t, "at-limit", getServiceName(tree, node.ActionIndex))

	overLimit := "example.com" + strings.Repeat("/*", rtree.MaxWildcardsPerRoute) + "/**"
	assert.PanicsWithValue(t,
		fmt.Sprintf("[rtree] route %q has %d wildcards, exceeds the limit of %d", overLimit, rtree.MaxWildcardsPerRoute+1, rtree.MaxWildcardsPerRoute),
		func() { rtree.Build([]*rtree.RawNode{{URL: overLimit, Service: "over-limit", Methods: "GET"}}) },
	)
}

func TestFallbackAcrossWildcardsRestoresCursor(t *testing.T) {
	deepStatic := "/a/b" + strings.Repeat("/*", 10) + "/c"
	deepWildcard := "/a/*" + strings.Repeat("/*", 10) + "/d"

	cases := []struct {
		routes []string
		url    string
		want   string
	}{
		{[]string{"/a/b/*/*/*/c", "/a/*/*/*/d"}, "/a/b/x/x/d", "/a/*/*/*/d"},
		{[]string{"/a/b/*/*/*/c", "/a/*/*/*/d"}, "/a/b/xx/yyy/zzzz/c", "/a/b/*/*/*/c"},
		{[]string{"/a/b/*/c", "/a/**/d"}, "/a/b/xx/yy/d", "/a/**/d"},
		{[]string{"/a/b/c/*/x", "/a/b/**/y"}, "/a/b/c/qq/y", "/a/b/**/y"},
		{[]string{"/a/bc/*/*/x", "/a/*/*/*/y"}, "/a/bc/qq/rr/y", "/a/*/*/*/y"},
		{[]string{"/a/*/b/*/c", "/a/*/**"}, "/a/xx/b/yy/z", "/a/*/**"},
		{[]string{deepStatic, deepWildcard}, "/a/b" + strings.Repeat("/x", 10) + "/d", deepWildcard},
		{[]string{deepStatic, deepWildcard}, "/a/b" + strings.Repeat("/x", 10) + "/c", deepStatic},
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("%v %s", c.routes, c.url), func(t *testing.T) {
			rawNodes := make([]*rtree.RawNode, 0, len(c.routes))
			for _, route := range c.routes {
				rawNodes = append(rawNodes, &rtree.RawNode{URL: "example.com" + route, Service: route, Methods: "GET"})
			}

			tree := rtree.Build(rawNodes)
			require.NotNil(t, tree)

			node, exists := tree.Search(prepareURL("example.com" + c.url))
			require.True(t, exists)
			assert.Equal(t, c.want, getServiceName(tree, node.ActionIndex))
		})
	}
}

func TestDuplicateRouteWarnsOnOverride(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	tree := rtree.Build([]*rtree.RawNode{
		{URL: "example.com/x", Service: "getter", Methods: "GET"},
		{URL: "example.com/y", Service: "other", Methods: "GET"},
		{URL: "example.com/x", Service: "poster", Methods: "POST"},
	})
	require.NotNil(t, tree)

	assert.Contains(t, logs.String(), `[rtree] Warning: route "example.com/x" [POST] overrides previous route "example.com/x" [GET]`)
	assert.Equal(t, 1, strings.Count(logs.String(), "Warning"))
}
