package builtins

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArguments(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"foo, bar", []string{"foo", "bar"}},
		{`"quoted value", plain`, []string{"quoted value", "plain"}},
		{"single", []string{"single"}},
		{"a, b, c", []string{"a", "b", "c"}},
		{`"with,comma"`, []string{"with,comma"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseArguments(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestParseDirective(t *testing.T) {
	tests := []struct {
		input    string
		wantName string
		wantArgs []string
	}{
		{"$SetHeader", "$SetHeader", nil},
		{"$SetHeader(X-Foo, bar)", "$SetHeader", []string{"X-Foo", "bar"}},
		{`$ok("hello world")`, "$ok", []string{"hello world"}},
		{"$err(418, I am a teapot)", "$err", []string{"418", "I am a teapot"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			name, args, err := ParseDirective(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantArgs, args)
		})
	}
}

func TestCheckArgCount(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		min     int
		max     int
		wantErr bool
	}{
		{"within range", []string{"a", "b"}, 1, 2, false},
		{"exact match", []string{"a"}, 1, 1, false},
		{"too few", []string{}, 1, 2, true},
		{"too many", []string{"a", "b", "c"}, 1, 2, true},
		{"too few, exact", []string{}, 2, 2, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := CheckArgCount(tt.args, tt.min, tt.max)
			assert.Equal(t, len(tt.args), n)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
