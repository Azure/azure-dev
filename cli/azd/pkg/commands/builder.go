package commands

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
)

// Create the core context for use in all Azd commands
// Registers context values for azCli, formatter, writer, console and more.
func RegisterDependenciesInCtx(ctx context.Context, cmd *cobra.Command, rootOptions *internal.GlobalCommandOptions) (context.Context, error) {
	// Set the global options in the go context
	ctx = internal.WithCommandOptions(ctx, *rootOptions)

	azCliArgs := azcli.NewAzCliArgs{
		EnableDebug:     rootOptions.EnableDebugLogging,
		EnableTelemetry: rootOptions.EnableTelemetry,
	}

	// Set default credentials used for operations against azure data/control planes
	credentials, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic("failed creating default azure credentials")
	}
	ctx = identity.WithCredentials(ctx, credentials)

	// Create and set the AzCli that will be used for the command
	azCli := azcli.NewAzCli(azCliArgs)
	ctx = azcli.WithAzCli(ctx, azCli)

	// Attempt to get the user specified formatter from the command args
	formatter, err := output.GetCommandFormatter(cmd)
	if err != nil {
		return ctx, err
	}

	if formatter != nil {
		ctx = output.WithFormatter(ctx, formatter)
	}

	writer := output.GetDefaultWriter()
	ctx = output.WithWriter(ctx, writer)

	console := input.NewConsole(!rootOptions.NoPrompt, writer, formatter)
	ctx = input.WithConsole(ctx, console)

	return ctx, nil
}
