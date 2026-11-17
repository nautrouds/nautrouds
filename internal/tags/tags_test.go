package tags

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnalyze(t *testing.T) {
	tests := []struct {
		name   string
		input  []string
		expect uint16
	}{
		{"nil", nil, 0},
		{"empty", []string{}, 0},
		{"unknown tag", []string{"@foo"}, 0},
		{"@!metrics", []string{"@!metrics"}, NoMetricsTag},
		{"@no-metrics", []string{"@no-metrics"}, NoMetricsTag},
		{"multiple with match", []string{"@!metrics", "@bar"}, NoMetricsTag},
		{"duplicate no-metrics", []string{"@!metrics", "@!metrics"}, NoMetricsTag},
		{"no @ prefix still matches", []string{"no-metrics"}, NoMetricsTag},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, Analyze(tt.input))
		})
	}
}
