package commands

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
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

	// Set the global options in the go context
	ctx = internal.WithCommandOptions(ctx, *rootOptions)
	ctx = tools.WithInstalledCheckCache(ctx)

	runner := exec.NewCommandRunner(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	ctx = exec.WithCommandRunner(ctx, runner)

	// Inject console into context
	ctx = input.WithConsole(ctx, console)
	return ctx, nil
}

var _ azcore.TokenCredential = &panicCredential{}

type panicCredential struct{}

func (pc *panicCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	panic("this command should not have attempted to call GetToken, it was marked as not requiring login")
}
