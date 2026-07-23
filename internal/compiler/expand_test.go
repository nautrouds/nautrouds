package compiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandField(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Simple Pipe",
			input:    "api-[v1|v2]",
			expected: []string{"api-v1", "api-v2"},
		},
		{
			name:     "Nested Brackets",
			input:    "www.[a|b[1|2]].com",
			expected: []string{"www.a.com", "www.b1.com", "www.b2.com"},
		},
		{
			name:     "Multiple Groups",
			input:    "[a|b].[v1|v2].io",
			expected: []string{"a.v1.io", "a.v2.io", "b.v1.io", "b.v2.io"},
		},
		{
			name:     "Empty Option",
			input:    "api/v1[/|]",
			expected: []string{"api/v1/", "api/v1"},
		},
		{
			name:     "No Brackets",
			input:    "example.com/api",
			expected: []string{"example.com/api"},
		},
		{
			name:     "Complex Mixed",
			input:    "src-[a|b]-[1|2]",
			expected: []string{"src-a-1", "src-a-2", "src-b-1", "src-b-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := expandField(tt.input)
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expected, res)
		})
	}
}

func TestExpandField_Escaping(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Escaped Brackets Literal",
			input:    `\[literal\].io/api`,
			expected: []string{"[literal].io/api"},
		},
		{
			name:     "Escaped Pipe Literal",
			input:    `[a\|b]`,
			expected: []string{"a|b"},
		},
		{
			name:     "Mixed Real And Escaped Group",
			input:    `[a|\[b\]]`,
			expected: []string{"a", "[b]"},
		},
		{
			name:     "Escaped Backslash",
			input:    `foo\\bar`,
			expected: []string{`foo\bar`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := expandField(tt.input)
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expected, res)
		})
	}
}

func TestExpandField_UnclosedBracket(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"Simple Unclosed", "foo[bar"},
		{"Nested Unclosed", "www.[a|b[1|2].com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := expandField(tt.input)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "unclosed")
		})
	}
}

func TestSplitByPipe(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"a|b|c", []string{"a", "b", "c"}},
		{"a[1|2]|b", []string{"a[1|2]", "b"}},
		{"", []string{""}},
	}

	for _, tt := range tests {
		res := splitByPipe(tt.input)
		assert.Equal(t, tt.expected, res)
	}
}
