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
	expectedPorts := []Port{
		{80, "tcp"},
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))

	ports, err = parsePorts(" 80 3100")
	expectedPorts = []Port{
		{80, "tcp"},
		{3100, "tcp"},
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))

	ports, err = parsePorts("80 3100/udp")
	expectedPorts = []Port{
		{80, "tcp"},
		{3100, "udp"},
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))

	ports, err = parsePorts(" 80/tcp 3100/udp ")
	expectedPorts = []Port{
		{80, "tcp"},
		{3100, "udp"},
	}
	require.Equal(t, nil, err)
	require.Equal(t, true, reflect.DeepEqual(ports, expectedPorts))
}
