package compiler

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_LineNumberedErrors(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		wantErr string
	}{
		{
			"InvalidRuleSyntax",
			"GET /path service extra\n",
			"line 1:",
		},
		{
			"UnknownTag",
			"GET /path service\n  @does-not-exist\n",
			"line 2: unknown tag",
		},
		{
			"UnknownBuiltinMiddleware",
			"GET /path service\n  $NonExistentFunc()\n",
			"line 2: unknown builtin middleware",
		},
		{
			"BlankLineThenBadTag",
			"GET /path service\n\n  @does-not-exist\n",
			"line 3: unknown tag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.script))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestParse_UnterminatedMiddleware(t *testing.T) {
	t.Run("FollowedByRule", func(t *testing.T) {
		script := "GET /path service\n  $SetHeader(\n    \"a\"\nGET /other other-service\n"
		_, err := Parse(strings.NewReader(script))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "line 4:")
		assert.Contains(t, err.Error(), "unterminated parentheses")
	})

	t.Run("FollowedByTag", func(t *testing.T) {
		script := "GET /path service\n  $SetHeader(\n    \"a\"\n  @no-metrics\n"
		_, err := Parse(strings.NewReader(script))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "line 4:")
		assert.Contains(t, err.Error(), "unterminated parentheses")
	})

	t.Run("AtEOF", func(t *testing.T) {
		script := "GET /path service\n  $SetHeader(\n    \"a\"\n"
		_, err := Parse(strings.NewReader(script))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unterminated parentheses")
	})
}

func TestParse_MultilineMiddleware(t *testing.T) {
	script := "GET /path service\n  $SetHeader(\n    \"X-Forwarded-For\",\n    \"Nautrouds\"\n  )\n"
	tree, err := Parse(strings.NewReader(script))
	require.NoError(t, err)
	require.NotNil(t, tree)
}
