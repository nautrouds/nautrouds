package ruleline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsRule(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{"ThreeFields", "GET /path service", true},
		{"IndentedSpaces", "  $SetHeader(a, b)", false},
		{"IndentedTab", "\t$SetHeader(a, b)", false},
		{"Blank", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsRule(tt.line))
		})
	}
}

func TestParse(t *testing.T) {
	t.Run("SingleField", func(t *testing.T) {
		tr := New()
		err := tr.Parse("service")
		assert.NoError(t, err)
		node := tr.Flush()
		assert.Equal(t, "*", node.Methods)
		assert.Equal(t, []string{"**"}, node.URLs)
		assert.Equal(t, "service", node.Service)
	})

	t.Run("TwoFields", func(t *testing.T) {
		tr := New()
		err := tr.Parse("/path service")
		assert.NoError(t, err)
		node := tr.Flush()
		assert.Equal(t, "*", node.Methods)
		assert.Equal(t, []string{"*/path"}, node.URLs)
		assert.Equal(t, "service", node.Service)
	})

	t.Run("ThreeFields", func(t *testing.T) {
		tr := New()
		err := tr.Parse("GET /path service")
		assert.NoError(t, err)
		node := tr.Flush()
		assert.Equal(t, "GET", node.Methods)
		assert.Equal(t, []string{"*/path"}, node.URLs)
		assert.Equal(t, "service", node.Service)
	})

	t.Run("ExpandsBracketField", func(t *testing.T) {
		tr := New()
		err := tr.Parse("/api-[v1|v2] service")
		assert.NoError(t, err)
		node := tr.Flush()
		assert.ElementsMatch(t, []string{"*/api-v1", "*/api-v2"}, node.URLs)
	})

	t.Run("UnclosedBracketField", func(t *testing.T) {
		tr := New()
		err := tr.Parse("/api-[v1 service")
		assert.Error(t, err)
		assert.Nil(t, tr.Flush())
	})

	t.Run("TooManyFields", func(t *testing.T) {
		tr := New()
		err := tr.Parse("GET /path service extra")
		assert.Error(t, err)
		assert.Nil(t, tr.Flush())
	})

	t.Run("UnknownMethod", func(t *testing.T) {
		tr := New()
		err := tr.Parse("BADMETHOD /path service")
		assert.Error(t, err)
		assert.Nil(t, tr.Flush())
	})

	t.Run("ValidVirtualService", func(t *testing.T) {
		tr := New()
		err := tr.Parse("/echo $echo")
		assert.NoError(t, err)
		assert.Equal(t, "$echo", tr.Flush().Service)
	})

	t.Run("UnknownVirtualService", func(t *testing.T) {
		tr := New()
		err := tr.Parse("/nope $doesnotexist")
		assert.Error(t, err)
		assert.Nil(t, tr.Flush())
	})

	t.Run("KeepsLastParsedOnFailure", func(t *testing.T) {
		tr := New()
		assert.NoError(t, tr.Parse("GET /path service"))

		assert.Error(t, tr.Parse("BADMETHOD /other other-service"))
		assert.Equal(t, "service", tr.Flush().Service)
	})
}
