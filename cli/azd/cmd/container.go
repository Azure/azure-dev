package cmd

// Run `go generate ./cmd` or `wire ./cmd` after modifying this file to regenerate `wire_gen.go`.

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/golobby/container/v3"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// Registers a singleton instance for the specified type
func registerInstance[F any](ioc container.Container, instance F) {
	container.MustSingletonLazy(ioc, func() F {
		return instance
	})
}

// Registers a singleton action for the specified action name
// This finds the action for a named instance and casts it to the correct type for injection
func registerAction[T any](ioc container.Container, actionName string) {
	container.MustSingletonLazy(ioc, func() (T, error) {
		var zero T
		var action actions.Action
		err := ioc.NamedResolve(&action, actionName)
		if err != nil {
			return zero, err
		}

		instance, ok := action.(T)
		if !ok {
			return zero, fmt.Errorf("failed converting action to initAction")
		}

		return instance, nil
	})
}

// Registers common Azd dependencies
func registerCommonDependencies(ioc container.Container) {
	container.MustSingletonLazy(ioc, output.GetCommandFormatter)

	container.MustSingletonLazy(ioc, func(
		rootOptions *internal.GlobalCommandOptions,
		formatter output.Formatter,
		cmd *cobra.Command) input.Console {
		writer := cmd.OutOrStdout()
		// When using JSON formatting, we want to ensure we always write messages from the console to stderr.
		if formatter != nil && formatter.Kind() == output.JsonFormat {
			writer = cmd.ErrOrStderr()
		}

		if os.Getenv("NO_COLOR") != "" {
			writer = colorable.NewNonColorable(writer)
		}

		isTerminal := cmd.OutOrStdout() == os.Stdout &&
			cmd.InOrStdin() == os.Stdin && isatty.IsTerminal(os.Stdin.Fd()) &&
			isatty.IsTerminal(os.Stdout.Fd())

		return input.NewConsole(rootOptions.NoPrompt, isTerminal, writer, input.ConsoleHandles{
			Stdin:  cmd.InOrStdin(),
			Stdout: cmd.OutOrStdout(),
			Stderr: cmd.ErrOrStderr(),
		}, formatter)
	})

	container.MustSingletonLazy(ioc, func(console input.Console) exec.CommandRunner {
		return exec.NewCommandRunner(
			console.Handles().Stdin,
			console.Handles().Stdout,
			console.Handles().Stderr,
		)
	})

	// Tools
	container.MustSingletonLazy(ioc, git.NewGitCli)
	container.MustSingletonLazy(ioc, func(rootOptions *internal.GlobalCommandOptions,
		credential azcore.TokenCredential) azcli.AzCli {
		return azcli.NewAzCli(credential, azcli.NewAzCliArgs{
			EnableDebug:     rootOptions.EnableDebugLogging,
			EnableTelemetry: rootOptions.EnableTelemetry,
			HttpClient:      nil,
		})
	})

	container.MustSingletonLazy(ioc, azdcontext.NewAzdContext)

	container.MustSingletonLazy(ioc, func(ctx context.Context, authManager *auth.Manager) (azcore.TokenCredential, error) {
		credential, err := authManager.CredentialForCurrentUser(ctx)
		if err != nil {
			return nil, err
		}

		if _, err := auth.EnsureLoggedInCredential(ctx, credential); err != nil {
			return nil, err
		}

		return credential, nil
	})

	container.MustSingletonLazy(ioc, func(console input.Console) io.Writer {
		writer := console.Handles().Stdout

		if os.Getenv("NO_COLOR") != "" {
			writer = colorable.NewNonColorable(writer)
		}

		return writer
	})

	container.MustSingletonLazy(ioc, config.NewUserConfigManager)
	container.MustSingletonLazy(ioc, config.NewManager)
	container.MustSingletonLazy(ioc, templates.NewTemplateManager)
	container.MustSingletonLazy(ioc, auth.NewManager)
	container.MustSingletonLazy(ioc, account.NewManager)

	// Required for nested actions called from composite actions like 'up'
	container.MustSingletonLazy(ioc, newInitAction)
	container.MustSingletonLazy(ioc, newDeployAction)
	container.MustSingletonLazy(ioc, newInfraCreateAction)

	registerAction[*initAction](ioc, "init-action")
	registerAction[*deployAction](ioc, "deploy-action")
	registerAction[*infraCreateAction](ioc, "infra-create-action")
}
