package appdetect

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestParsePorts(t *testing.T) {
	tests := []struct {
		portString    string
		expectedPorts []Port
	}{
		{"", nil},
		{"80", []Port{{80, "tcp"}}},
		{"80 3100", []Port{{80, "tcp"}, {3100, "tcp"}}},
		{"80 3100/udp", []Port{{80, "tcp"}, {3100, "udp"}}},
		{" 80/tcp 3100/udp ", []Port{{80, "tcp"}, {3100, "udp"}}},
	}
	for _, tt := range tests {
		t.Run(tt.portString, func(t *testing.T) {
			actual, err := parsePorts(tt.portString)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedPorts, actual)
		})
	}
}
