package tagline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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

	ok, err := tr.TryParse("  @no-metrics")
	assert.True(t, ok)
	assert.NoError(t, err)

	ok, err = tr.TryParse("@!metrics")
	assert.True(t, ok)
	assert.NoError(t, err)

	ok, err = tr.TryParse("  $SetHeader(a, b)")
	assert.False(t, ok)
	assert.NoError(t, err)

	ok, err = tr.TryParse("GET /path service")
	assert.False(t, ok)
	assert.NoError(t, err)

	assert.Equal(t, []string{"@no-metrics", "@!metrics"}, tr.Flush())
}

func TestTryParse_UnknownTag(t *testing.T) {
	tr := New()

	ok, err := tr.TryParse("@does-not-exist")
	assert.True(t, ok)
	assert.Error(t, err)
	assert.Empty(t, tr.Flush())
}

func TestFlush(t *testing.T) {
	tr := New()
	_, _ = tr.TryParse("@no-metrics")

	assert.Equal(t, []string{"@no-metrics"}, tr.Flush())
	assert.Empty(t, tr.Flush())
}
