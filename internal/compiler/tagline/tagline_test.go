package tagline

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

func TestIsTag(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{"Tag", "@no-metrics", true},
		{"IndentedTag", "  @no-metrics", true},
		{"Middleware", "  $SetHeader(a, b)", false},
		{"Rule", "GET /path service", false},
		{"Blank", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsTag(tt.line))
		})
	}
}

func TestTryParse(t *testing.T) {
	tr := New()

	ok, err := tr.TryParse(tokenizeOne(t, "  @no-metrics"))
	assert.True(t, ok)
	assert.NoError(t, err)

	ok, err = tr.TryParse(tokenizeOne(t, "@!metrics"))
	assert.True(t, ok)
	assert.NoError(t, err)

	ok, err = tr.TryParse(tokenizeOne(t, "  $SetHeader(a, b)"))
	assert.False(t, ok)
	assert.NoError(t, err)

	ok, err = tr.TryParse(tokenizeOne(t, "GET /path service"))
	assert.False(t, ok)
	assert.NoError(t, err)

	assert.Equal(t, []string{"@no-metrics", "@!metrics"}, tr.Flush())
}

func TestTryParse_UnknownTag(t *testing.T) {
	tr := New()

	ok, err := tr.TryParse(tokenizeOne(t, "@does-not-exist"))
	assert.True(t, ok)
	assert.Error(t, err)
	assert.Empty(t, tr.Flush())
}

func TestFlush(t *testing.T) {
	tr := New()
	_, _ = tr.TryParse(tokenizeOne(t, "@no-metrics"))

	assert.Equal(t, []string{"@no-metrics"}, tr.Flush())
	assert.Empty(t, tr.Flush())
}
