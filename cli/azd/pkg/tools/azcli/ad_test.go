package azcli

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
	"github.com/stretchr/testify/require"
)

func Test_CreateOrUpdateServicePrincipal(t *testing.T) {
	// TODO: Not really a test, just debugging implementation. Will fix.
	ctx := context.Background()
	azCliCred, err := azidentity.NewAzureCLICredential(nil)
	require.NoError(t, err)

	ctx = identity.WithCredentials(ctx, azCliCred)
	cli := NewAzCli(azCliCred, NewAzCliArgs{
		EnableDebug:     true,
		EnableTelemetry: false,
		CommandRunner:   exec.NewCommandRunner(os.Stdin, os.Stdout, os.Stderr),
		HttpClient:      http.DefaultClient,
	})

	spnCreds, err := cli.CreateOrUpdateServicePrincipal(
		ctx,
		"faa080af-c1d8-40ad-9cce-e1a450ca5b57",
		"wabrez-spn-go-test",
		"Contributor",
	)
	require.NoError(t, err)
	require.NotNil(t, spnCreds)
}
