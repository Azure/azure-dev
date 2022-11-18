package commands

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"

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

	ctx = tools.WithInstalledCheckCache(ctx)

	// Inject console into context
	ctx = input.WithConsole(ctx, console)
	return ctx, nil
}
