package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/repository"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/containerapps"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	infraBicep "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	infraTerraform "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/terraform"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/javac"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/kubectl"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/npm"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/swa"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/terraform"
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

	container.RegisterSingleton(func(console input.Console, rootOptions *internal.GlobalCommandOptions) exec.CommandRunner {
		return exec.NewCommandRunner(
			&exec.RunnerOptions{
				Stdin:        console.Handles().Stdin,
				Stdout:       console.Handles().Stdout,
				Stderr:       console.Handles().Stderr,
				DebugLogging: rootOptions.EnableDebugLogging,
			})
	})
	container.RegisterSingleton(input.NewConsoleMessaging)

	client := createHttpClient()
	container.RegisterSingleton(func() httputil.HttpClient { return client })
	container.RegisterSingleton(func() auth.HttpClient { return client })

	// Auth
	container.RegisterSingleton(auth.NewLoggedInGuard)
	container.RegisterSingleton(auth.NewMultiTenantCredentialProvider)
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

	container.RegisterSingleton(func(cmd *cobra.Command) envFlag {
		envValue, err := cmd.Flags().GetString(environmentNameFlag)
		if err != nil {
			panic("command asked for envFlag, but envFlag was not included in cmd.Flags().")
		}

		return envFlag{environmentName: envValue}
	})

	container.RegisterSingleton(func(cmd *cobra.Command) CmdAnnotations {
		return cmd.Annotations
	})

	// Azd Context
	container.RegisterSingleton(azdcontext.NewAzdContext)

	// Lazy loads the Azd context after the azure.yaml file becomes available
	container.RegisterSingleton(func() *lazy.Lazy[*azdcontext.AzdContext] {
		return lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
			return azdcontext.NewAzdContext()
		})
	})

	// Register an initialized environment based on the specified environment flag, or the default environment.
	// Note that referencing an *environment.Environment in a command automatically triggers a UI prompt if the
	// environment is uninitialized or a default environment doesn't yet exist.
	container.RegisterSingleton(
		func(ctx context.Context,
			azdContext *azdcontext.AzdContext,
			lazyEnv *lazy.Lazy[*environment.Environment],
			envFlags envFlag,
			console input.Console,
		) (*environment.Environment, error) {
			if azdContext == nil {
				return nil, azdcontext.ErrNoProject
			}

			environmentName := envFlags.environmentName
			var err error

			env, err := loadOrCreateEnvironment(ctx, environmentName, azdContext, console)
			if err != nil {
				return nil, fmt.Errorf("loading environment: %w", err)
			}

			// Reset lazy env value after loading or creating environment
			// This allows any previous lazy instances (such as hooks) to now point to the same instance
			lazyEnv.SetValue(env)

			return env, nil
		},
	)
	container.RegisterSingleton(func() environment.EnvironmentResolver {
		return func() (*environment.Environment, error) { return loadEnvironmentIfAvailable() }
	})

	// Lazy loads an existing environment, erroring out if not available
	// One can repeatedly call GetValue to wait until the environment is available.
	container.RegisterSingleton(
		func(lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext], envFlags envFlag) *lazy.Lazy[*environment.Environment] {
			return lazy.NewLazy(func() (*environment.Environment, error) {
				azdCtx, err := lazyAzdContext.GetValue()
				if err != nil {
					return nil, err
				}

				environmentName := envFlags.environmentName
				if environmentName == "" {
					environmentName, err = azdCtx.GetDefaultEnvironmentName()
					if err != nil {
						return nil, err
					}
				}

				env, err := environment.GetEnvironment(azdCtx, environmentName)
				if err != nil {
					return nil, err
				}

				return env, err
			})
		},
	)

	// Project Config
	container.RegisterSingleton(
		func(ctx context.Context, azdContext *azdcontext.AzdContext) (*project.ProjectConfig, error) {
			if azdContext == nil {
				return nil, azdcontext.ErrNoProject
			}

			projectConfig, err := project.Load(ctx, azdContext.ProjectPath())
			if err != nil {
				return nil, err
			}

			return projectConfig, nil
		},
	)

	// Lazy loads the project config from the Azd Context when it becomes available
	container.RegisterSingleton(func(lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]) *lazy.Lazy[*project.ProjectConfig] {
		return lazy.NewLazy(func() (*project.ProjectConfig, error) {
			_, err := lazyAzdContext.GetValue()
			if err != nil {
				return nil, err
			}

			var projectConfig *project.ProjectConfig
			err = container.Resolve(&projectConfig)

			return projectConfig, err
		})
	})

	container.RegisterSingleton(project.NewResourceManager)
	container.RegisterSingleton(project.NewProjectManager)
	container.RegisterSingleton(project.NewServiceManager)
	container.RegisterSingleton(repository.NewInitializer)
	container.RegisterSingleton(config.NewUserConfigManager)
	container.RegisterSingleton(alpha.NewFeaturesManager)
	container.RegisterSingleton(config.NewManager)
	container.RegisterSingleton(templates.NewTemplateManager)
	container.RegisterSingleton(auth.NewManager)
	container.RegisterSingleton(azcli.NewUserProfileService)
	container.RegisterSingleton(account.NewSubscriptionsService)
	container.RegisterSingleton(account.NewManager)
	container.RegisterSingleton(account.NewSubscriptionsManager)
	container.RegisterSingleton(account.NewSubscriptionCredentialProvider)
	container.RegisterSingleton(azcli.NewManagedClustersService)
	container.RegisterSingleton(azcli.NewContainerRegistryService)
	container.RegisterSingleton(containerapps.NewContainerAppService)
	container.RegisterSingleton(project.NewContainerHelper)
	container.RegisterSingleton(azcli.NewSpringService)
	container.RegisterSingleton(func() ioc.ServiceLocator {
		return ioc.NewServiceLocator(container)
	})

	container.RegisterSingleton(func(subManager *account.SubscriptionsManager) account.SubscriptionTenantResolver {
		return subManager
	})

	// Tools
	container.RegisterSingleton(func(
		rootOptions *internal.GlobalCommandOptions,
		credentialProvider account.SubscriptionCredentialProvider,
		httpClient httputil.HttpClient,
	) azcli.AzCli {
		return azcli.NewAzCli(credentialProvider, httpClient, azcli.NewAzCliArgs{
			EnableDebug:     rootOptions.EnableDebugLogging,
			EnableTelemetry: rootOptions.EnableTelemetry,
		})
	})
	container.RegisterSingleton(bicep.NewBicepCli)
	container.RegisterSingleton(docker.NewDocker)
	container.RegisterSingleton(dotnet.NewDotNetCli)
	container.RegisterSingleton(git.NewGitCli)
	container.RegisterSingleton(github.NewGitHubCli)
	container.RegisterSingleton(javac.NewCli)
	container.RegisterSingleton(kubectl.NewKubectl)
	container.RegisterSingleton(maven.NewMavenCli)
	container.RegisterSingleton(npm.NewNpmCli)
	container.RegisterSingleton(python.NewPythonCli)
	container.RegisterSingleton(swa.NewSwaCli)
	container.RegisterSingleton(terraform.NewTerraformCli)

	// Provisioning
	container.RegisterTransient(provisioning.NewManager)
	container.RegisterSingleton(provisioning.NewPrincipalIdProvider)
	container.RegisterSingleton(prompt.NewDefaultPrompter)

	// Provisioning Providers
	provisionProviderMap := map[provisioning.ProviderKind]any{
		provisioning.Bicep:     infraBicep.NewBicepProvider,
		provisioning.Terraform: infraTerraform.NewTerraformProvider,
	}

	for provider, constructor := range provisionProviderMap {
		if err := container.RegisterNamedTransient(string(provider), constructor); err != nil {
			panic(fmt.Errorf("registering IaC provider %s: %w", provider, err))
		}
	}

	// Other
	container.RegisterSingleton(createClock)

	// Service Targets
	serviceTargetMap := map[project.ServiceTargetKind]any{
		"":                          project.NewAppServiceTarget,
		project.AppServiceTarget:    project.NewAppServiceTarget,
		project.AzureFunctionTarget: project.NewFunctionAppTarget,
		project.ContainerAppTarget:  project.NewContainerAppTarget,
		project.StaticWebAppTarget:  project.NewStaticWebAppTarget,
		project.AksTarget:           project.NewAksTarget,
		project.SpringAppTarget:     project.NewSpringAppTarget,
	}

	for target, constructor := range serviceTargetMap {
		if err := container.RegisterNamedSingleton(string(target), constructor); err != nil {
			panic(fmt.Errorf("registering service target %s: %w", target, err))
		}
	}

	// Languages
	frameworkServiceMap := map[project.ServiceLanguageKind]any{
		"":                                project.NewDotNetProject,
		project.ServiceLanguageDotNet:     project.NewDotNetProject,
		project.ServiceLanguageCsharp:     project.NewDotNetProject,
		project.ServiceLanguageFsharp:     project.NewDotNetProject,
		project.ServiceLanguagePython:     project.NewPythonProject,
		project.ServiceLanguageJavaScript: project.NewNpmProject,
		project.ServiceLanguageTypeScript: project.NewNpmProject,
		project.ServiceLanguageJava:       project.NewMavenProject,
		project.ServiceLanguageDocker:     project.NewDockerProject,
	}

	for language, constructor := range frameworkServiceMap {
		if err := container.RegisterNamedSingleton(string(language), constructor); err != nil {
			panic(fmt.Errorf("registering framework service %s: %w", language, err))
		}
	}

	// Pipelines
	container.RegisterSingleton(pipeline.NewPipelineManager)
	container.RegisterSingleton(func(flags *pipelineConfigFlags) *pipeline.PipelineManagerArgs {
		return &flags.PipelineManagerArgs
	})

	pipelineProviderMap := map[string]any{
		"github-ci":  pipeline.NewGitHubCiProvider,
		"github-scm": pipeline.NewGitHubScmProvider,
		"azdo-ci":    pipeline.NewAzdoCiProvider,
		"azdo-scm":   pipeline.NewAzdoScmProvider,
	}

	for provider, constructor := range pipelineProviderMap {
		if err := container.RegisterNamedSingleton(string(provider), constructor); err != nil {
			panic(fmt.Errorf("registering pipeline provider %s: %w", provider, err))
		}
	}

	// Required for nested actions called from composite actions like 'up'
	registerActionInitializer[*initAction](container, "azd-init-action")
	registerActionInitializer[*provisionAction](container, "azd-provision-action")
	registerActionInitializer[*restoreAction](container, "azd-restore-action")
	registerActionInitializer[*buildAction](container, "azd-build-action")
	registerActionInitializer[*packageAction](container, "azd-package-action")
	registerActionInitializer[*deployAction](container, "azd-deploy-action")

	registerAction[*provisionAction](container, "azd-provision-action")
	registerAction[*downAction](container, "azd-down-action")
}
