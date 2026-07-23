package rtree_test

import (
	"nautrouds/internal/rtree"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateMethods_ValidTokens(t *testing.T) {
	tests := []string{
		"g", "get", "GET",
		"p", "po", "post",
		"pu", "put",
		"d", "del", "delete",
		"head", "connect", "options", "trace", "patch",
		"*", "any",
		"GET,POST",
		" get , post ",
	}

	for _, methods := range tests {
		t.Run(methods, func(t *testing.T) {
			ok, bad := rtree.ValidateMethods(methods)
			assert.True(t, ok)
			assert.Empty(t, bad)
		})
	}
}

func TestValidateMethods_Empty(t *testing.T) {
	ok, bad := rtree.ValidateMethods("")
	assert.True(t, ok)
	assert.Empty(t, bad)
}

func TestValidateMethods_BadToken(t *testing.T) {
	ok, bad := rtree.ValidateMethods("GRT")
	assert.False(t, ok)
	assert.Equal(t, "GRT", bad)

	ok, bad = rtree.ValidateMethods("GET,PSOT")
	assert.False(t, ok)
	assert.Equal(t, "PSOT", bad)
}
