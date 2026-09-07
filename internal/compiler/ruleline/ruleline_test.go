package ruleline

import (
	"testing"

	"nautrouds/internal/compiler/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tokenizeOne(t *testing.T, line string) *tokenizer.Part {
	t.Helper()
	parts, err := tokenizer.New().Tokenize(1, line)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	return parts[0]
}

func TestIsRule(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{"ThreeFields", "GET /path service", true},
		{"IndentedSpaces", "  $SetHeader(a, b)", false},
		{"IndentedTab", "\t$SetHeader(a, b)", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsRule(tokenizeOne(t, tt.line)))
		})
	}
}

func TestParse(t *testing.T) {
	parse := func(t *testing.T, tr *Tracker, line string) error {
		t.Helper()
		return tr.Parse(tokenizeOne(t, line))
	}

	t.Run("SingleField", func(t *testing.T) {
		tr := New()
		err := parse(t, tr, "service")
		assert.NoError(t, err)
		node := tr.Flush()
		assert.Equal(t, "*", node.Methods)
		assert.Equal(t, []string{"**"}, node.URLs)
		assert.Equal(t, "service", node.Service)
	})

	t.Run("TwoFields", func(t *testing.T) {
		tr := New()
		err := parse(t, tr, "/path service")
		assert.NoError(t, err)
		node := tr.Flush()
		assert.Equal(t, "*", node.Methods)
		assert.Equal(t, []string{"*/path"}, node.URLs)
		assert.Equal(t, "service", node.Service)
	})

	t.Run("ThreeFields", func(t *testing.T) {
		tr := New()
		err := parse(t, tr, "GET /path service")
		assert.NoError(t, err)
		node := tr.Flush()
		assert.Equal(t, "GET", node.Methods)
		assert.Equal(t, []string{"*/path"}, node.URLs)
		assert.Equal(t, "service", node.Service)
	})

	t.Run("ExpandsBracketField", func(t *testing.T) {
		tr := New()
		err := parse(t, tr, "/api-[v1|v2] service")
		assert.NoError(t, err)
		node := tr.Flush()
		assert.ElementsMatch(t, []string{"*/api-v1", "*/api-v2"}, node.URLs)
	})

	t.Run("UnclosedBracketField", func(t *testing.T) {
		tr := New()
		err := parse(t, tr, "/api-[v1 service")
		assert.Error(t, err)
		assert.Nil(t, tr.Flush())
	})

	t.Run("TooManyFields", func(t *testing.T) {
		tr := New()
		err := parse(t, tr, "GET /path service extra")
		assert.Error(t, err)
		assert.Nil(t, tr.Flush())
	})

	t.Run("UnknownMethod", func(t *testing.T) {
		tr := New()
		err := parse(t, tr, "BADMETHOD /path service")
		assert.Error(t, err)
		assert.Nil(t, tr.Flush())
	})

	t.Run("ValidVirtualService", func(t *testing.T) {
		tr := New()
		err := parse(t, tr, "/echo $echo")
		assert.NoError(t, err)
		assert.Equal(t, "$echo", tr.Flush().Service)
	})

	t.Run("UnknownVirtualService", func(t *testing.T) {
		tr := New()
		err := parse(t, tr, "/nope $doesnotexist")
		assert.Error(t, err)
		assert.Nil(t, tr.Flush())
	})

	t.Run("KeepsLastParsedOnFailure", func(t *testing.T) {
		tr := New()
		assert.NoError(t, parse(t, tr, "GET /path service"))

		assert.Error(t, parse(t, tr, "BADMETHOD /other other-service"))
		assert.Equal(t, "service", tr.Flush().Service)
	})
}
