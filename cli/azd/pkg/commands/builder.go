package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"

	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// Create the core context for use in all Azd commands
// Registers context values for azCli, formatter, writer, console and more.
func RegisterDependenciesInCtx(
	ctx context.Context,
	cmd *cobra.Command,
	rootOptions *internal.GlobalCommandOptions,
) (context.Context, error) {

	// Set the global options in the go context
	ctx = internal.WithCommandOptions(ctx, *rootOptions)
	ctx = tools.WithInstalledCheckCache(ctx)

	runner := exec.NewCommandRunner(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	ctx = exec.WithCommandRunner(ctx, runner)

	writer := cmd.OutOrStdout()

	if os.Getenv("NO_COLOR") != "" {
		writer = colorable.NewNonColorable(writer)
	}

	authManager, err := auth.NewManager(writer, config.NewManager())
	if err != nil {
		return ctx, fmt.Errorf("creating auth manager: %w", err)
	}

	var credential azcore.TokenCredential

	// TODO(ellismg): This is a hack so that we don't fail for `login` when we construct the root context if a user
	// is not logged in. This is super fragile, but we should be able to clean it up soon with Wei's work.
	if cmd.Use != "login" && cmd.Use != "logout" && cmd.Use != "version" {
		cred, err := authManager.GetCredentialForCurrentUser(ctx)
		if err != nil {
			return ctx, fmt.Errorf("fetching current user: %w", err)
		}
		credential = cred
	} else {
		credential = &panicCredential{}
	}

	azCliArgs := azcli.NewAzCliArgs{
		EnableDebug:     rootOptions.EnableDebugLogging,
		EnableTelemetry: rootOptions.EnableTelemetry,
		CommandRunner:   runner,
	}

	// Set default credentials used for operations against azure data/control planes
	ctx = identity.WithCredentials(ctx, credential)

	// Create and set the AzCli that will be used for the command
	azCli := azcli.NewAzCli(credential, azCliArgs)
	ctx = azcli.WithAzCli(ctx, azCli)

	// Attempt to get the user specified formatter from the command args
	formatter, err := output.GetCommandFormatter(cmd)
	if err != nil {
		return ctx, err
	}

	if formatter != nil {
		ctx = output.WithFormatter(ctx, formatter)
	}

	ctx = output.WithWriter(ctx, writer)

	isTerminal := cmd.OutOrStdout() == os.Stdout &&
		cmd.InOrStdin() == os.Stdin && isatty.IsTerminal(os.Stdin.Fd()) &&
		isatty.IsTerminal(os.Stdout.Fd())

	// When using JSON formatting, we want to ensure we always write messages from the console to stderr.
	if formatter != nil && formatter.Kind() == output.JsonFormat {
		writer = cmd.ErrOrStderr()
	}

	console := input.NewConsole(rootOptions.NoPrompt, isTerminal, writer, input.ConsoleHandles{
		Stdin:  cmd.InOrStdin(),
		Stdout: cmd.OutOrStdout(),
		Stderr: cmd.ErrOrStderr(),
	}, formatter)
	ctx = input.WithConsole(ctx, console)

	return ctx, nil
}

var _ azcore.TokenCredential = &panicCredential{}

type panicCredential struct{}

func (pc *panicCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	panic("should not have been used")
}
