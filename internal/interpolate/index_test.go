package interpolate_test

import (
	"nautrouds/internal/interpolate"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnalyze(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []uint32
	}{
		{
			name:  "Single tag - host",
			input: "{host}",
			expected: []uint32{
				interpolate.OpHost, 0, 6,
			},
		},
		{
			name:  "Multiple tags",
			input: "{host}/{path}?{queries}",
			expected: []uint32{
				interpolate.OpHost, 0, 6,
				interpolate.OpPath, 7, 13,
				interpolate.OpQueries, 14, 23,
			},
		},
		{
			name:  "Query and Header tags",
			input: "{query.id}-{header.X-API-Key}",
			expected: []uint32{
				interpolate.OpQuery, 0, 10,
				interpolate.OpHeader, 11, 29,
			},
		},
		{
			name:  "Case insensitivity",
			input: "{HOST}",
			expected: []uint32{
				interpolate.OpHost, 0, 6,
			},
		},
		{
			name:  "Method and RemoteIP",
			input: "{method} {remoteip}",
			expected: []uint32{
				interpolate.OpMethod, 0, 8,
				interpolate.OpRemoteIP, 9, 19,
			},
		},
		{
			name:  "Path and RawURI",
			input: "{path} {rawuri}",
			expected: []uint32{
				interpolate.OpPath, 0, 6,
				interpolate.OpRawURI, 7, 15,
			},
		},
		{
			name:     "No tags",
			input:    "plain text",
			expected: []uint32{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := interpolate.Analyze(tt.input)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestReplace(t *testing.T) {
	u, _ := url.Parse("http://example.com/api/v1?id=123&name=nautrouds")
	req, _ := http.NewRequest("GET", u.String(), nil)
	req.Host = "example.com"
	req.Header.Set("X-API-Key", "secret-key")
	req.RemoteAddr = "192.168.1.1:1234"

	ctx := interpolate.New(req)

	tests := []struct {
		name     string
		origin   string
		expected string
	}{
		{
			name:     "Full replacement",
			origin:   "http://{host}{path}?{queries}",
			expected: "http://example.com/api/v1?id=123&name=nautrouds",
		},
		{
			name:     "Method and RemoteIP",
			origin:   "[{method}] from {remoteip}",
			expected: "[GET] from 192.168.1.1",
		},
		{
			name:     "Query and Header",
			origin:   "User {query.id} with key {header.X-API-Key}",
			expected: "User 123 with key secret-key",
		},
		{
			name:     "Static text only",
			origin:   "static text",
			expected: "static text",
		},
		{
			name:     "Non-existent query/header",
			origin:   "Empty: {query.none} and {header.X-None}",
			expected: "Empty:  and ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := interpolate.Analyze(tt.origin)
			actual := ctx.Replace(tt.origin, metadata)
			assert.Equal(t, tt.expected, actual)
		})
	}
}
