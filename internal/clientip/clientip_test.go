package clientip_test

import (
	"net/http/httptest"
	"testing"

	"nautrouds/internal/clientip"

	"github.com/stretchr/testify/assert"
)

func TestFromRequest(t *testing.T) {
	tests := []struct {
		forwarded []string
		want      string
	}{
		{forwarded: nil, want: ""},
		{forwarded: []string{"10.0.0.1, 203.0.113.9"}, want: "203.0.113.9"},
		{forwarded: []string{"10.0.0.1", "203.0.113.9"}, want: "203.0.113.9"},
		{forwarded: []string{"[2001:db8::1]:8080"}, want: "2001:db8::1"},
		{forwarded: []string{"203.0.113.9, unknown"}, want: ""},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		for _, h := range tt.forwarded {
			req.Header.Add("X-Forwarded-For", h)
		}
		assert.Equal(t, tt.want, clientip.FromRequest(req), tt.forwarded)
	}
}
