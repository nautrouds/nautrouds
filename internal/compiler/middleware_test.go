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
