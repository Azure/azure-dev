package dotnet

import (
	"context"
	_ "embed"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/publish-with-workload-update-message.txt
var publishWithWorkloadUpdate string

func Test_getTargetPort(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())

	cli := &dotNetCli{
		commandRunner: mockCtx.CommandRunner,
	}

	port, err := cli.getTargetPort(publishWithWorkloadUpdate, "")
	require.NoError(t, err)
	require.Equal(t, 8080, port)

	_, err = cli.getTargetPort("", "")
	require.Error(t, err)

	_, err = cli.getTargetPort(
		"\r\nWorkload updates are available. Run `dotnet workload list` for more information.\r\n", "")
	require.Error(t, err)
}
