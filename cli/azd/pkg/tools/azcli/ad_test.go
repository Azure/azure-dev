package azcli

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/stretchr/testify/require"
)

func Test_CreateOrUpdateServicePrincipal(t *testing.T) {
	ctx := context.Background()
	azcli := NewAzCli(NewAzCliArgs{
		EnableDebug:     true,
		EnableTelemetry: false,
		CommandRunner:   exec.NewCommandRunner(os.Stdin, os.Stdout, os.Stderr),
		HttpClient:      http.DefaultClient,
	})

	creds, err := azcli.CreateOrUpdateServicePrincipal(ctx, "faa080af-c1d8-40ad-9cce-e1a450ca5b57", "wabrez-spn-go-test", "Contributor")
	require.NoError(t, err)
	require.NotNil(t, creds)
}
