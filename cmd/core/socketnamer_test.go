package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSocketNamer(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		wantFormat string
		notOwned   []string
	}{
		{"NoInstanceID", "", "nautrouds-0.sock", []string{"nautrouds-abc-0.sock"}},
		{"WithInstanceID", "abc", "nautrouds-abc-0.sock", []string{"nautrouds-abcd-0.sock", "nautrouds-0.sock"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namer := newSocketNamer(tt.instanceID)

			assert.Equal(t, tt.wantFormat, namer.Format(0))
			assert.True(t, namer.Owns(tt.wantFormat))
			for _, name := range tt.notOwned {
				assert.False(t, namer.Owns(name), name)
			}
		})
	}
}
