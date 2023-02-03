package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
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

// Registers a singleton action initializer for the specified action name
// This returns a function that when called resolves the action
// This is to ensure pre-conditions are met for composite actions like 'up'
// This finds the action for a named instance and casts it to the correct type for injection
func registerAction[T actions.Action](container *ioc.NestedContainer, actionName string) {
	container.RegisterSingleton(func() (T, error) {
		return resolveAction[T](container, actionName)
	})
}

// Registers a singleton action for the specified action name
// This finds the action for a named instance and casts it to the correct type for injection
func registerActionInitializer[T actions.Action](container *ioc.NestedContainer, actionName string) {
	container.RegisterSingleton(func() actions.ActionInitializer[T] {
		return func() (T, error) {
			return resolveAction[T](container, actionName)
		}
	})
}

// Resolves the action instance for the specified action name
// This finds the action for a named instance and casts it to the correct type for injection
func resolveAction[T actions.Action](container *ioc.NestedContainer, actionName string) (T, error) {
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
	container.RegisterSingleton(input.NewConsoleMessaging)

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
	container.RegisterSingleton(func() httputil.HttpClient { return &http.Client{} })

	container.RegisterSingleton(auth.NewMultiTenantCredentialProvider)
	// Register a default azcore.TokenCredential that is selected based on the environment's subscription.
	container.RegisterSingleton(
		func(
			ctx context.Context,
			env *environment.Environment,
			accountSub *account.SubscriptionsManager,
			credProvider auth.MultiTenantCredentialProvider) (azcore.TokenCredential, error) {
			if env == nil {
				return nil, fmt.Errorf("an environment wasn't selected")
			}

			subscriptionId := env.GetSubscriptionId()
			if subscriptionId == "" {
				return nil, fmt.Errorf(
					"environment %s does not have %s set",
					env.GetEnvName(), environment.SubscriptionIdEnvVarName)
			}

			tenantId, err := accountSub.ResolveTenant(ctx, subscriptionId)
			if err != nil {
				return nil, err
			}

			return credProvider.GetTokenCredential(ctx, tenantId)
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
		func(ctx context.Context,
			azdContext *azdcontext.AzdContext,
			envFlags flagsWithEnv,
			console input.Console,
			accountManager account.Manager,
			userProfileService *azcli.UserProfileService) (*environment.Environment, error) {
			if azdContext == nil {
				return nil, azdcontext.ErrNoProject
			}

			environmentName := envFlags.EnvironmentName()
			var err error

			env, err := loadOrInitEnvironment(
				ctx, &environmentName, azdContext, console, accountManager, userProfileService)
			if err != nil {
				return nil, fmt.Errorf("loading environment: %w", err)
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
	container.RegisterSingleton(azcli.NewUserProfileService)
	container.RegisterSingleton(azcli.NewSubscriptionsService)
	container.RegisterSingleton(account.NewManager)
	container.RegisterSingleton(account.NewSubscriptionsManager)
	container.RegisterSingleton(func(subManager *account.SubscriptionsManager) account.SubscriptionTenantResolver {
		return subManager
	})

	// Required for nested actions called from composite actions like 'up'
	registerActionInitializer[*initAction](container, "azd-init-action")
	registerActionInitializer[*deployAction](container, "azd-deploy-action")
	registerActionInitializer[*infraCreateAction](container, "azd-infra-create-action")
	// Required for alias actions like 'provision' and 'down'
	registerAction[*infraCreateAction](container, "azd-infra-create-action")
	registerAction[*infraDeleteAction](container, "azd-infra-delete-action")
}
