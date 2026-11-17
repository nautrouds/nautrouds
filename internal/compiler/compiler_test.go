package compiler_test

import (
	"fmt"
	"nautrouds/internal/compiler"
	"nautrouds/internal/rtree"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func search(t *testing.T, tree *rtree.RouteTree, rawURL string) *rtree.RouteNode {
	t.Helper()
	url := []byte(rawURL)
	rtree.ReverseHost(url)

	node, exists := tree.Search(url)
	require.True(t, exists, rawURL)
	return node
}

func serviceOf(tree *rtree.RouteTree, node *rtree.RouteNode) string {
	serviceIndex := tree.ActionMetadata[node.ActionIndex]
	return tree.GetActionName(tree.ActionMetadata[serviceIndex])
}

func middlewaresOf(tree *rtree.RouteTree, node *rtree.RouteNode) []string {
	mwCount := tree.ActionMetadata[node.ActionIndex+1]
	var mws []string
	for i := range mwCount {
		mwMetaIndex := tree.ActionMetadata[node.ActionIndex+2+i]
		mwName := tree.GetActionName(tree.ActionMetadata[mwMetaIndex])
		mws = append(mws, mwName)
	}
	return mws
}

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
		node := search(t, tree, "example.com/api/v1")
		assert.Equal(t, "svc-1", serviceOf(tree, node))
		assert.ElementsMatch(t, []string{"mw-auth", "mw-log"}, middlewaresOf(tree, node))
	})

	t.Run("Method Filtering", func(t *testing.T) {
		node := search(t, tree, "example.com/api/v2")
		assert.Equal(t, rtree.MethodGet, node.Methods&rtree.MethodGet)
	})

	t.Run("Default Method Star", func(t *testing.T) {
		node := search(t, tree, "example.com/api/v3")
		assert.Equal(t, rtree.MethodAny, node.Methods)
	})
}

func TestParse_WithExpansion(t *testing.T) {
	tree, err := compiler.ParseString("GET [a|b].io/api svc-expanded")
	require.NoError(t, err)

	for _, url := range []string{"a.io/api", "b.io/api"} {
		node := search(t, tree, url)
		assert.Equal(t, "svc-expanded", serviceOf(tree, node))
	}
}

func TestParse_Middlewares(t *testing.T) {
	tests := []struct {
		name        string
		middlewares string
		want        []string
	}{
		{"Mmfg", "$mmfg(mmfg-service/echo)", []string{"$mmfg(mmfg-service/echo)"}},
		{"MmfgQuotedNodeName", `$mmfg("mmfg-service/my node (echo)")`, []string{`$mmfg("mmfg-service/my node (echo)")`}},
		{"External", "auth-service(/check, header=X-User-Id)", []string{"auth-service(/check, header=X-User-Id)"}},
		{"BodySizeLimitAsLast", "$SetHeader(a, b)\n    $BodySizeLimit(1KB)", []string{"$SetHeader(a, b)", "$BodySizeLimit(1KB)"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := fmt.Sprintf("GET example.com/api svc\n    %s\n", tt.middlewares)
			tree, err := compiler.ParseString(script)
			require.NoError(t, err)
			require.NotNil(t, tree)

			node := search(t, tree, "example.com/api")
			assert.ElementsMatch(t, tt.want, middlewaresOf(tree, node))
		})
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		wantErrs []string
	}{
		{
			name:     "InvalidBuiltin",
			script:   "\nGET example.com/api svc\n    $NonExistentFunc()\n",
			wantErrs: []string{"unknown builtin middleware"},
		},
		{
			name:     "InvalidMethod",
			script:   "\nGRT example.com/api svc\n",
			wantErrs: []string{"unknown HTTP method: GRT"},
		},
		{
			name:     "InvalidMethodInCommaList",
			script:   "\nGET,PSOT example.com/api svc\n",
			wantErrs: []string{"unknown HTTP method: PSOT"},
		},
		{
			name:     "InvalidBuiltinArgCount",
			script:   "\nGET example.com/api svc\n    $SetHeader(a, b, c)\n",
			wantErrs: []string{"$SetHeader"},
		},
		{
			name:     "InvalidVirtualServiceArgCount",
			script:   "\nGET example.com/api $redirect(301)\n",
			wantErrs: []string{"$redirect"},
		},
		{
			name:     "InvalidExternalMiddleware",
			script:   "\nGET example.com/api svc\n    auth-service(/check\n",
			wantErrs: []string{"line 3"},
		},
		{
			name:     "UnclosedBracketInURL",
			script:   "\nGET example.com/api svc-1\n\nGET example.[com svc-2\n",
			wantErrs: []string{"line 4", "unclosed"},
		},
		// Unclosed parens are treated as a pending multi-line directive; with nothing left to close them, parsing ends in "unterminated", not an $mmfg-specific error.
		{
			name:     "Mmfg/MissingClosingParen",
			script:   "\nGET example.com/api svc\n    $mmfg(\n",
			wantErrs: []string{"unterminated parentheses"},
		},
		{
			name:     "Mmfg/UnterminatedNodeName",
			script:   "\nGET example.com/api svc\n    $mmfg(foo\n",
			wantErrs: []string{"unterminated parentheses"},
		},
		{
			name:     "Mmfg/EmptyNodeName",
			script:   "\nGET example.com/api svc\n    $mmfg()\n",
			wantErrs: []string{"invalid $mmfg"},
		},
		{
			name:     "Mmfg/NoParens",
			script:   "\nGET example.com/api svc\n    $mmfg\n",
			wantErrs: []string{"invalid $mmfg"},
		},
		{
			name:     "Mmfg/TooManyArgs",
			script:   "\nGET example.com/api svc\n    $mmfg(foo, bar)\n",
			wantErrs: []string{"invalid $mmfg"},
		},
		{
			name:     "BodySizeLimit/FollowedByBuiltin",
			script:   "\nGET example.com/api svc\n    $BodySizeLimit(1KB)\n    $SetHeader(a, b)\n",
			wantErrs: []string{"must be the last middleware", "line 4"},
		},
		{
			name:     "BodySizeLimit/FollowedByMmfg",
			script:   "\nGET example.com/api svc\n    $BodySizeLimit(1KB)\n    $mmfg(node)\n",
			wantErrs: []string{"must be the last middleware", "line 4"},
		},
		{
			name:     "BodySizeLimit/FollowedByExternal",
			script:   "\nGET example.com/api svc\n    $BodySizeLimit(1KB)\n    auth-service\n",
			wantErrs: []string{"must be the last middleware", "line 4"},
		},
		{
			name:     "LineNumbered/InvalidRuleSyntax",
			script:   "GET /path service extra\n",
			wantErrs: []string{"line 1:"},
		},
		{
			name:     "LineNumbered/UnknownTag",
			script:   "GET /path service\n  @does-not-exist\n",
			wantErrs: []string{"line 2: unknown tag"},
		},
		{
			name:     "LineNumbered/UnknownBuiltinMiddleware",
			script:   "GET /path service\n  $NonExistentFunc()\n",
			wantErrs: []string{"line 2: unknown builtin middleware"},
		},
		{
			name:     "LineNumbered/BlankLineThenBadTag",
			script:   "GET /path service\n\n  @does-not-exist\n",
			wantErrs: []string{"line 3: unknown tag"},
		},
		{
			name:     "UnterminatedMiddleware/FollowedByRule",
			script:   "GET /path service\n  $SetHeader(\n    \"a\"\nGET /other other-service\n",
			wantErrs: []string{"line 4:", "unterminated parentheses"},
		},
		{
			name:     "UnterminatedMiddleware/FollowedByTag",
			script:   "GET /path service\n  $SetHeader(\n    \"a\"\n  @no-metrics\n",
			wantErrs: []string{"line 4:", "unterminated parentheses"},
		},
		{
			name:     "UnterminatedMiddleware/AtEOF",
			script:   "GET /path service\n  $SetHeader(\n    \"a\"\n",
			wantErrs: []string{"unterminated parentheses"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := compiler.ParseString(tt.script)
			require.Error(t, err)
			for _, want := range tt.wantErrs {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

func TestParse_MultilineMiddleware(t *testing.T) {
	tree, err := compiler.ParseString("GET /path service\n  $SetHeader(\n    \"X-Forwarded-For\",\n    \"Nautrouds\"\n  )\n")
	require.NoError(t, err)
	assert.NotNil(t, tree)
}
