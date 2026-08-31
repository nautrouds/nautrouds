package commentline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsComment(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{"Rule", "GET /path service", false},
		{"Indented", "  $SetHeader(a, b)", false},
		{"LineComment", "# note", true},
		{"LineCommentNoSpace", "#note", true},
		{"BlockStart", "#* block", true},
		{"Blank", "", false},
		{"QuotedHash", `"#literal"`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := New()
			assert.Equal(t, tt.want, d.IsComment(tt.line))
		})
	}
}

func TestIsComment_BlockSkipUntilBlank(t *testing.T) {
	d := New()

	assert.True(t, d.IsComment("#* start"))
	assert.True(t, d.IsComment("still inside block"))
	assert.False(t, d.IsComment(""))
	assert.False(t, d.IsComment("GET /path service"))
}
