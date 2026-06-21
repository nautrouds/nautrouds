package tags

import "testing"

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
			got := Analyze(tt.input)
			if got != tt.expect {
				t.Errorf("Analyze(%v) = %d, want %d", tt.input, got, tt.expect)
			}
		})
	}
}

func TestNoMetricsTag_IsBit(t *testing.T) {
	if NoMetricsTag == 0 {
		t.Error("NoMetricsTag must be non-zero")
	}
	if NoMetricsTag&(NoMetricsTag-1) != 0 {
		t.Error("NoMetricsTag must be a power of 2 (single bit)")
	}
}
