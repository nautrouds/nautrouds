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

type routeCase struct {
	routes []string
	url    string
	want   string
}

func runRouteCases(t *testing.T, cases []routeCase) {
	for _, tt := range cases {
		name := fmt.Sprintf("%v %s", tt.routes, tt.url)
		t.Run(name, func(t *testing.T) {
			rawNodes := make([]*rtree.RawNode, 0, len(tt.routes))
			for _, route := range tt.routes {
				rawNodes = append(rawNodes, &rtree.RawNode{URL: route, Service: route, Methods: "GET"})
			}

			tree := rtree.Build(rawNodes)
			require.NotNil(t, tree)

			urlBytes := prepareURL(tt.url)
			node, exists := tree.Search(urlBytes)
			if tt.want == "" {
				assert.False(t, exists)
				return
			}

			require.True(t, exists)
			assert.Equal(t, tt.want, getServiceName(tree, node.ActionIndex))
		})
	}
}

func prefixed(cases []routeCase) []routeCase {
	for i := range cases {
		for j := range cases[i].routes {
			cases[i].routes[j] = "example.com" + cases[i].routes[j]
		}
		cases[i].url = "example.com" + cases[i].url
		if cases[i].want != "" {
			cases[i].want = "example.com" + cases[i].want
		}
	}
	return cases
}

func TestStressExtremeWildcards(t *testing.T) {
	rawNodes := []*rtree.RawNode{}

	for i := range 50 {
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

	rawNodes = append(rawNodes, &rtree.RawNode{URL: "domain0.com/api/v0/**", Service: "greedy-global", Methods: "GET"})
	rawNodes = append(rawNodes, &rtree.RawNode{URL: "domain0.com/api/v0/*/*/resource_0", Service: "specific-match", Methods: "GET"})

	tree := rtree.Build(rawNodes)
	require.NotNil(t, tree)

	url := prepareURL("domain0.com/api/v0/a/b/resource_0")
	node, exists := tree.Search(url)
	assert.True(t, exists)
	assert.Equal(t, "specific-match", getServiceName(tree, node.ActionIndex))

	url2 := prepareURL("domain0.com/api/v0/some/deep/path/that/is/not/specific")
	node2, exists2 := tree.Search(url2)
	assert.True(t, exists2)
	assert.Equal(t, "greedy-global", getServiceName(tree, node2.ActionIndex))
}

func TestWildcardHostAndBacktracking(t *testing.T) {
	runRouteCases(t, []routeCase{
		{
			routes: []string{"*.com/api/*/*/details", "*.com/api/*/**"},
			url:    "example.com/api/v1/v2/details",
			want:   "*.com/api/*/*/details",
		},
		{
			routes: []string{"*.com/api/*/*/details", "*.com/api/*/**"},
			url:    "example.com/api/v1/v2/corrupted",
			want:   "*.com/api/*/**",
		},
		{
			routes: []string{"*.com/*/assets/images/logo.png", "*.com/static/assets/images/logo.png"},
			url:    "example.com/static/assets/images/wrong.png",
		},
		{
			routes: []string{"*.com/*/assets/images/logo.png", "*.com/static/assets/images/logo.png"},
			url:    "example.com/alex/assets/images/logo.png",
			want:   "*.com/*/assets/images/logo.png",
		},
		{
			routes: []string{"*/api/v1/status"},
			url:    "sub.deep.internal.enterprise.co.uk/api/v1/status",
			want:   "*/api/v1/status",
		},
	})
}

func TestStaticLeafFallsBackToWildcard(t *testing.T) {
	cases := []routeCase{
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

	prefixedCases := prefixed(cases)
	runRouteCases(t, prefixedCases)
}

func TestWildcardLeafMatchPriority(t *testing.T) {
	cases := []routeCase{
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

	prefixedCases := prefixed(cases)
	runRouteCases(t, prefixedCases)
}

func TestWildcardLimitPerRoute(t *testing.T) {
	atLimit := "example.com" + strings.Repeat("/*", rtree.MaxWildcardsPerRoute)
	tree := rtree.Build([]*rtree.RawNode{{URL: atLimit, Service: "at-limit", Methods: "GET"}})
	require.NotNil(t, tree)

	matchingURL := "example.com" + strings.Repeat("/x", rtree.MaxWildcardsPerRoute)
	urlBytes := prepareURL(matchingURL)
	node, exists := tree.Search(urlBytes)
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

	cases := []routeCase{
		{[]string{"/a/b/*/*/*/c", "/a/*/*/*/d"}, "/a/b/x/x/d", "/a/*/*/*/d"},
		{[]string{"/a/b/*/*/*/c", "/a/*/*/*/d"}, "/a/b/xx/yyy/zzzz/c", "/a/b/*/*/*/c"},
		{[]string{"/a/b/*/c", "/a/**/d"}, "/a/b/xx/yy/d", "/a/**/d"},
		{[]string{"/a/b/c/*/x", "/a/b/**/y"}, "/a/b/c/qq/y", "/a/b/**/y"},
		{[]string{"/a/bc/*/*/x", "/a/*/*/*/y"}, "/a/bc/qq/rr/y", "/a/*/*/*/y"},
		{[]string{"/a/*/b/*/c", "/a/*/**"}, "/a/xx/b/yy/z", "/a/*/**"},
		{[]string{deepStatic, deepWildcard}, "/a/b" + strings.Repeat("/x", 10) + "/d", deepWildcard},
		{[]string{deepStatic, deepWildcard}, "/a/b" + strings.Repeat("/x", 10) + "/c", deepStatic},
	}

	prefixedCases := prefixed(cases)
	runRouteCases(t, prefixedCases)
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

	output := logs.String()
	assert.Contains(t, output, `[rtree] Warning: route "example.com/x" [POST] overrides previous route "example.com/x" [GET]`)
	assert.Equal(t, 1, strings.Count(output, "Warning"))
}
