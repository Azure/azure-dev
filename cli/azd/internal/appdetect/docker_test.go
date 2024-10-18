package appdetect

import (
	"github.com/stretchr/testify/require"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectPortInDockerfile(t *testing.T) {

	ports, err := getExposedPorts(filepath.Join("testdata", "Dockerfile", "DockerfileNoPort"))
	require.Equal(t, nil, err)
	require.Equal(t, 0, len(ports))

	ports, err = getExposedPorts(filepath.Join("testdata", "Dockerfile", "DockerfilePort80"))
	expectedPorts := map[int]string{
		80: "tcp",
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))

	ports, err = getExposedPorts(filepath.Join("testdata", "Dockerfile",
		"DockerfilePort80And3100InOneExposeSameProtocol"))
	expectedPorts = map[int]string{
		80:   "tcp",
		3100: "tcp",
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))

	ports, err = getExposedPorts(filepath.Join("testdata", "Dockerfile",
		"DockerfilePort80And3100InOneExposeDifferentProtocol"))
	expectedPorts = map[int]string{
		80:   "tcp",
		3100: "udp",
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))

	ports, err = getExposedPorts(filepath.Join("testdata", "Dockerfile",
		"DockerfilePort80And3100InMultiExposeSameProtocol"))
	expectedPorts = map[int]string{
		80:   "tcp",
		3100: "tcp",
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))

	ports, err = getExposedPorts(filepath.Join("testdata", "Dockerfile",
		"DockerfilePort80And3100InMultiExposeDifferentProtocol"))
	expectedPorts = map[int]string{
		80:   "tcp",
		3100: "udp",
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))
}
