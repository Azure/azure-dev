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

	authManager, err := auth.NewManager(config.NewManager())
	if err != nil {
		return ctx, fmt.Errorf("creating auth manager: %w", err)
	}

	var credential azcore.TokenCredential

	if _, has := cmd.Annotations[RequireNoLoginAnnotation]; has {
		credential = &panicCredential{}
	} else {
		cred, err := authManager.CredentialForCurrentUser(ctx)
		if err != nil {
			return ctx, fmt.Errorf("fetching current user: %w", err)
		}
		if _, err := auth.EnsureLoggedInCredential(ctx, cred); err != nil {
			return ctx, err
		}
		credential = cred
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
	panic("this command should not have attempted to call GetToken, it was marked as not requiring login")
}

// RequireNoLoginAnnotation may be set as an annotation on a `cobra.Command` to instruct the command package that the
// command does not require the user be logged in. This is used to prevent a login check from running when adding a
// credential to the root context object. If code with this annotation ends up trying to use the credentials object
// which is placed on the context, it will panic.
//
// TODO(azure/azure-dev#899): We can remove this logic and all of it's uses when we no longer register a credential
// in the context with [RegisterDependenciesInCtx].
const RequireNoLoginAnnotation = "github.com/azure/azure-dev/cli/azd/pkg/commands/requireNoLoginAnnotation"
