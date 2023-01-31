package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/repository"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// Registers a singleton action for the specified action name
// This finds the action for a named instance and casts it to the correct type for injection
func registerAction[T any](container *ioc.NestedContainer, actionName string) {
	container.RegisterSingleton(func() (T, error) {
		var zero T
		var action actions.Action
		err := container.ResolveNamed(actionName, &action)
		if err != nil {
			return zero, err
		}

		instance, ok := action.(T)
		if !ok {
			return zero, fmt.Errorf("failed converting action to '%T'", zero)
		}

		return instance, nil
	})
}

// Registers common Azd dependencies
func registerCommonDependencies(container *ioc.NestedContainer) {
	container.RegisterSingleton(output.GetCommandFormatter)

	container.RegisterSingleton(func(
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

	container.RegisterSingleton(func(console input.Console) exec.CommandRunner {
		return exec.NewCommandRunner(
			console.Handles().Stdin,
			console.Handles().Stdout,
			console.Handles().Stderr,
		)
	})

	// Tools
	container.RegisterSingleton(git.NewGitCli)
	container.RegisterSingleton(func(rootOptions *internal.GlobalCommandOptions,
		credential azcore.TokenCredential) azcli.AzCli {
		return azcli.NewAzCli(credential, azcli.NewAzCliArgs{
			EnableDebug:     rootOptions.EnableDebugLogging,
			EnableTelemetry: rootOptions.EnableTelemetry,
			HttpClient:      nil,
		})
	})

	container.RegisterSingleton(azdcontext.NewAzdContext)

	container.RegisterSingleton(func(ctx context.Context, authManager *auth.Manager) (azcore.TokenCredential, error) {
		credential, err := authManager.CredentialForCurrentUser(ctx, nil)
		if err != nil {
			return nil, err
		}

		if _, err := auth.EnsureLoggedInCredential(ctx, credential); err != nil {
			return nil, err
		}

		return credential, nil
	})

	container.RegisterSingleton(func(mgr *auth.Manager) CredentialProviderFn {
		return mgr.CredentialForCurrentUser
	})

	container.RegisterSingleton(func(console input.Console) io.Writer {
		writer := console.Handles().Stdout

		if os.Getenv("NO_COLOR") != "" {
			writer = colorable.NewNonColorable(writer)
		}

		return writer
	})

	container.RegisterSingleton(func() flagsWithEnv {
		// Get the current cmd flags for the executing command
		var currentFlags flags
		err := container.Resolve(&currentFlags)
		if err != nil {
			return &envFlag{}
		}

		// Attempt to cast to flags with env
		flagsWithEnv, ok := currentFlags.(flagsWithEnv)
		if !ok {
			return &envFlag{}
		}

		return flagsWithEnv
	})

	container.RegisterSingleton(
		func(azdContext *azdcontext.AzdContext, envFlags flagsWithEnv) (*environment.Environment, error) {
			if azdContext == nil {
				return nil, azdcontext.ErrNoProject
			}

			environmentName := envFlags.EnvironmentName()
			var err error

			if environmentName == "" {
				defaultEnvName, err := azdContext.GetDefaultEnvironmentName()
				if err != nil {
					return nil, err
				}

				environmentName = defaultEnvName
			}

			env, err := environment.GetEnvironment(azdContext, environmentName)
			if err != nil {
				return nil, err
			}

			return env, nil
		},
	)

	container.RegisterSingleton(
		func(azdContext *azdcontext.AzdContext) (*project.ProjectConfig, error) {
			if azdContext == nil {
				return nil, azdcontext.ErrNoProject
			}

			projectConfig, err := project.LoadProjectConfig(azdContext.ProjectPath())
			if err != nil {
				return nil, err
			}

			return projectConfig, nil
		},
	)

	container.RegisterSingleton(repository.NewInitializer)
	container.RegisterSingleton(config.NewUserConfigManager)
	container.RegisterSingleton(config.NewManager)
	container.RegisterSingleton(templates.NewTemplateManager)
	container.RegisterSingleton(auth.NewManager)
	container.RegisterSingleton(account.NewManager)

	container.RegisterSingleton(newInitAction)
	container.RegisterSingleton(newDeployAction)
	container.RegisterSingleton(newInfraCreateAction)

	// Required for nested actions called from composite actions like 'up' and 'down'
	registerAction[*initAction](container, "azd-init-action")
	registerAction[*deployAction](container, "azd-deploy-action")
	registerAction[*infraCreateAction](container, "azd-infra-create-action")
	registerAction[*infraDeleteAction](container, "azd-infra-delete-action")
}
