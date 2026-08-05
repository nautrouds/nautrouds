package compiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateExternalMiddleware(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{"NoParens", "auth-service", false},
		{"ValidArgs", "auth-service(/check, header=X-User-Id)", false},
		{"MissingClosingParen", "auth-service(/check", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExternalMiddleware(tt.expr)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateMiddlewareOrder(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		newMw    string
		wantErr  bool
	}{
		{"Empty", nil, "$SetHeader(a, b)", false},
		{"NoBodySizeLimit", []string{"$SetHeader(a, b)"}, "$IPAllow(10.0.0.0/8)", false},
		{"AppendAfterBodySizeLimit_Duplicate", []string{"$SetHeader(a, b)", "$BodySizeLimit(1KB)"}, "$BodySizeLimit(1KB)", true},
		{"BodySizeLimitFollowedByBuiltin", []string{"$BodySizeLimit(1KB)"}, "$SetHeader(a, b)", true},
		{"BodySizeLimitFollowedByMmfg", []string{"$BodySizeLimit(1KB)"}, "$mmfg(node)", true},
		{"BodySizeLimitFollowedByExternal", []string{"$BodySizeLimit(1KB)"}, "auth-service", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMiddlewareOrder(tt.existing, tt.newMw)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
