package appdetect

import (
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

func TestDetectPortInDockerfile(t *testing.T) {
	var port []int
	port = detectPortInDockerfile(filepath.Join("testdata", "Dockerfile", "DockerfilePort80"))
	require.Equal(t, 1, len(port))
	require.Equal(t, 80, port[0])

	port = detectPortInDockerfile(filepath.Join("testdata", "Dockerfile", "DockerfilePort3100"))
	require.Equal(t, 1, len(port))
	require.Equal(t, 3100, port[0])

	port = detectPortInDockerfile(filepath.Join("testdata", "Dockerfile", "DockerfilePort3100And80"))
	require.Equal(t, 2, len(port))
	require.Equal(t, 3100, port[0])
	require.Equal(t, 80, port[1])

	port = detectPortInDockerfile(filepath.Join("testdata", "Dockerfile", "DockerfileNoPort"))
	require.Equal(t, 0, len(port))
}
