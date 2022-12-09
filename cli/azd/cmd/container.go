package cmd

// Run `go generate ./cmd` or `wire ./cmd` after modifying this file to regenerate `wire_gen.go`.

import (
	"context"
	"io"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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

func registerInstance[F any](ioc container.Container, instance F) {
	ioc.SingletonLazy(func() F {
		return instance
	})
}

func registerCommonDependencies(ioc container.Container) {
	ioc.SingletonLazy(output.GetCommandFormatter)

	ioc.SingletonLazy(func(
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

	ioc.SingletonLazy(func(console input.Console) exec.CommandRunner {
		return exec.NewCommandRunner(
			console.Handles().Stdin,
			console.Handles().Stdout,
			console.Handles().Stderr,
		)
	})

	// Tools
	ioc.SingletonLazy(git.NewGitCli)
	ioc.SingletonLazy(func(rootOptions *internal.GlobalCommandOptions,
		credential azcore.TokenCredential) azcli.AzCli {
		return azcli.NewAzCli(credential, azcli.NewAzCliArgs{
			EnableDebug:     rootOptions.EnableDebugLogging,
			EnableTelemetry: rootOptions.EnableTelemetry,
			HttpClient:      nil,
		})
	})

	ioc.SingletonLazy(azdcontext.NewAzdContext)

	ioc.SingletonLazy(func(ctx context.Context, authManager *auth.Manager) (azcore.TokenCredential, error) {
		credential, err := authManager.CredentialForCurrentUser(ctx)
		if err != nil {
			return nil, err
		}

		if _, err := auth.EnsureLoggedInCredential(ctx, credential); err != nil {
			return nil, err
		}

		return credential, nil
	})

	ioc.SingletonLazy(func(console input.Console) io.Writer {
		writer := console.Handles().Stdout

		if os.Getenv("NO_COLOR") != "" {
			writer = colorable.NewNonColorable(writer)
		}

		return writer
	})

	ioc.SingletonLazy(config.NewUserConfigManager)
	ioc.SingletonLazy(config.NewManager)
	ioc.SingletonLazy(templates.NewTemplateManager)
	ioc.SingletonLazy(auth.NewManager)
	ioc.SingletonLazy(account.NewManager)

	// Required for nested actions called from composite actions like 'up'
	ioc.SingletonLazy(newInitAction)
	ioc.SingletonLazy(newDeployAction)
	ioc.SingletonLazy(newInfraCreateAction)
}
