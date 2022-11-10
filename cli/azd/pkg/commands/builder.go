package commands

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"

	"github.com/spf13/cobra"
)

// Create the core context for use in all Azd commands
// Registers context values for azCli, formatter, writer, console and more.
func RegisterDependenciesInCtx(
	ctx context.Context,
	cmd *cobra.Command,
	console input.Console,
	rootOptions *internal.GlobalCommandOptions,
) (context.Context, error) {
	// Set the global options in the go context
	ctx = internal.WithCommandOptions(ctx, *rootOptions)
	ctx = tools.WithInstalledCheckCache(ctx)

	runner := exec.NewCommandRunner(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	ctx = exec.WithCommandRunner(ctx, runner)

	// Set default credentials used for operations against azure data/control planes
	credentials, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		panic("failed creating azure cli credential")
	}
	ctx = identity.WithCredentials(ctx, credentials)

	azCliArgs := azcli.NewAzCliArgs{
		EnableDebug:     rootOptions.EnableDebugLogging,
		EnableTelemetry: rootOptions.EnableTelemetry,
		CommandRunner:   runner,
	}

	// Create and set the AzCli that will be used for the command
	azCli := azcli.NewAzCli(credentials, azCliArgs)
	ctx = azcli.WithAzCli(ctx, azCli)

	// Inject console into context
	ctx = input.WithConsole(ctx, console)
	return ctx, nil
}
