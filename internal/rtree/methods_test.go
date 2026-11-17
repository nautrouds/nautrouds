package rtree_test

import (
	"nautrouds/internal/rtree"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateMethods(t *testing.T) {
	tests := []struct {
		methods string
		wantOK  bool
		wantBad string
	}{
		{"", true, ""},
		{"g", true, ""},
		{"get", true, ""},
		{"GET", true, ""},
		{"p", true, ""},
		{"po", true, ""},
		{"post", true, ""},
		{"pu", true, ""},
		{"put", true, ""},
		{"d", true, ""},
		{"del", true, ""},
		{"delete", true, ""},
		{"head", true, ""},
		{"connect", true, ""},
		{"options", true, ""},
		{"trace", true, ""},
		{"patch", true, ""},
		{"*", true, ""},
		{"any", true, ""},
		{"GET,POST", true, ""},
		{" get , post ", true, ""},
		{"GRT", false, "GRT"},
		{"GET,PSOT", false, "PSOT"},
	}

	for _, tt := range tests {
		t.Run(tt.methods, func(t *testing.T) {
			ok, bad := rtree.ValidateMethods(tt.methods)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantBad, bad)
		})
	}
}
