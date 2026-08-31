package ruleline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Leading Slash Gets Host Wildcard",
			input:    "/api/v1",
			expected: "*/api/v1",
		},
		{
			name:     "Trailing Single Star Upgraded To Greedy",
			input:    "api/v1/*",
			expected: "api/v1/**",
		},
		{
			name:     "Trailing Greedy Star Untouched",
			input:    "api/v1/**",
			expected: "api/v1/**",
		},
		{
			name:     "Leading Slash And Trailing Star Combined",
			input:    "/api/v1/*",
			expected: "*/api/v1/**",
		},
		{
			name:     "No Change Needed",
			input:    "example.com/api/v1",
			expected: "example.com/api/v1",
		},
		{
			name:     "Bare Star Upgraded",
			input:    "*",
			expected: "**",
		},
		{
			name:     "Host Only Gets Greedy Path",
			input:    "example.com",
			expected: "example.com/**",
		},
		{
			name:     "Pure Greedy Wildcard Untouched",
			input:    "**",
			expected: "**",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := normalizeURL(tt.input)
			assert.Equal(t, tt.expected, res)
		})
	}
}
