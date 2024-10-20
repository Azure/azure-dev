package appdetect

import (
	"github.com/stretchr/testify/require"
	"reflect"
	"testing"
)

func TestParsePorts(t *testing.T) {
	ports, err := parsePorts("")
	require.NotEqual(t, nil, err)
	require.Equal(t, 0, len(ports))

	ports, err = parsePorts(" 80")
	expectedPorts := []int{
		80,
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))

	ports, err = parsePorts(" 80 3100")
	expectedPorts = []int{
		80,
		3100,
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))

	ports, err = parsePorts("80 3100/udp")
	expectedPorts = []int{
		80,
		3100,
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))

	ports, err = parsePorts(" 80/tcp 3100/udp ")
	expectedPorts = []int{
		80,
		3100,
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))
}
