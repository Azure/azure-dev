package appdetect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/assert"
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

func TestAnalyzeDockerFromFile(t *testing.T) {
	tests := []struct {
		dockerFileContent string
		expectedPorts     []Port
	}{
		{"", nil},
		{"# EXPOSE 80", nil},
		{"EXPOSE 80", []Port{{80, "tcp"}}},
		{"EXPOSE 80 3100", []Port{{80, "tcp"}, {3100, "tcp"}}},
		{"EXPOSE 80\nEXPOSE 3100", []Port{{80, "tcp"}, {3100, "tcp"}}},
		{"EXPOSE 80/tcp\nEXPOSE 3100/udp", []Port{{80, "tcp"}, {3100, "udp"}}},
		{"\n  EXPOSE 80/tcp\n    EXPOSE 3100/udp", []Port{{80, "tcp"}, {3100, "udp"}}},
	}
	for _, tt := range tests {
		t.Run(tt.dockerFileContent, func(t *testing.T) {
			tempDir := t.TempDir()
			tempFile := filepath.Join(tempDir, "Dockerfile")
			file, err := os.Create(tempFile)
			assert.NoError(t, err)
			file.Close()

			err = os.WriteFile(tempFile, []byte(tt.dockerFileContent), osutil.PermissionFile)
			assert.NoError(t, err)

			docker, err := AnalyzeDocker(tempFile)
			assert.NoError(t, err)
			actual := docker.Ports
			assert.Equal(t, tt.expectedPorts, actual)
		})
	}
}
