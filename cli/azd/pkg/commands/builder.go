package commands

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
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

	authManager, err := auth.NewManager(config.NewUserConfigManager())
	if err != nil {
		return ctx, fmt.Errorf("creating auth manager: %w", err)
	}

	// Set default credentials used for operations against azure data/control planes
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

	ctx = identity.WithCredentials(ctx, credential)

	azCliArgs := azcli.NewAzCliArgs{
		EnableDebug:     rootOptions.EnableDebugLogging,
		EnableTelemetry: rootOptions.EnableTelemetry,
	}

	// Create and set the AzCli that will be used for the command
	azCli := azcli.NewAzCli(credential, azCliArgs)
	ctx = azcli.WithAzCli(ctx, azCli)

	// Inject console into context
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
