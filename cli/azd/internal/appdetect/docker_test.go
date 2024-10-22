package appdetect

import (
	"github.com/stretchr/testify/assert"
	"path/filepath"
	"testing"
)

func TestParsePortsInLine(t *testing.T) {
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
			actual, err := parsePortsInLine(tt.portString)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedPorts, actual)
		})
	}
}

func TestParsePortsInFile(t *testing.T) {
	tests := []struct {
		dockerFileName string
		expectedPorts  []Port
	}{
		{"DockerfileNoPort", nil},
		{"DockerfileSinglePort", []Port{{80, "tcp"}}},
		{"DockerfileMultiPortsInSingleLine", []Port{{80, "tcp"}, {3100, "tcp"}}},
		{"DockerfileMultiPortsInMultiLines", []Port{{80, "tcp"}, {3100, "tcp"}}},
		{"DockerfileMultiPortsInMultiLinesDifferentProtocol", []Port{{80, "tcp"}, {3100, "udp"}}},
		{"DockerfileMultiPortsInMultiLinesDifferentProtocolWithWhitespacePrefix", []Port{{80, "tcp"}, {3100, "udp"}}},
	}
	for _, tt := range tests {
		t.Run(tt.dockerFileName, func(t *testing.T) {
			fileName := filepath.Join("testdata", "Dockerfile", tt.dockerFileName)
			actual, err := parsePortsInFile(fileName)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedPorts, actual)
		})
	}
}
