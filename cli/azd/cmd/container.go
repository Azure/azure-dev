package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/internal/repository"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azd"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/containerapps"
	"github.com/azure/azure-dev/cli/azd/pkg/devcenter"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/helm"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/kubelogin"
	"github.com/azure/azure-dev/cli/azd/pkg/kustomize"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/state"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
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
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/mattn/go-colorable"
	"github.com/spf13/cobra"
	"golang.org/x/exp/slices"
)

// Registers a transient action initializer for the specified action name
// This returns a function that when called resolves the action
// This is to ensure pre-conditions are met for composite actions like 'up'
// This finds the action for a named instance and casts it to the correct type for injection
func registerAction[T actions.Action](container *ioc.NestedContainer, actionName string) {
	container.MustRegisterTransient(func(serviceLocator ioc.ServiceLocator) (T, error) {
		return resolveAction[T](serviceLocator, actionName)
	})
}

// Resolves the action instance for the specified action name
// This finds the action for a named instance and casts it to the correct type for injection
func resolveAction[T actions.Action](serviceLocator ioc.ServiceLocator, actionName string) (T, error) {
	var zero T
	var action actions.Action
	err := serviceLocator.ResolveNamed(actionName, &action)
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
	// Core bootstrapping registrations
	ioc.RegisterInstance(container, container)
	container.MustRegisterSingleton(NewCobraBuilder)
	container.MustRegisterSingleton(middleware.NewMiddlewareRunner)

	// Standard Registrations
	container.MustRegisterTransient(output.GetCommandFormatter)

	container.MustRegisterScoped(func(
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
			cmd.InOrStdin() == os.Stdin && input.IsTerminal(os.Stdout.Fd(), os.Stdin.Fd())

		return input.NewConsole(rootOptions.NoPrompt, isTerminal, writer, input.ConsoleHandles{
			Stdin:  cmd.InOrStdin(),
			Stdout: cmd.OutOrStdout(),
			Stderr: cmd.ErrOrStderr(),
		}, formatter)
	})

	container.MustRegisterSingleton(
		func(console input.Console, rootOptions *internal.GlobalCommandOptions) exec.CommandRunner {
			return exec.NewCommandRunner(
				&exec.RunnerOptions{
					Stdin:        console.Handles().Stdin,
					Stdout:       console.Handles().Stdout,
					Stderr:       console.Handles().Stderr,
					DebugLogging: rootOptions.EnableDebugLogging,
				})
		},
	)

	client := createHttpClient()
	ioc.RegisterInstance[httputil.HttpClient](container, client)
	ioc.RegisterInstance[auth.HttpClient](container, client)
	container.MustRegisterSingleton(func() httputil.UserAgent {
		return httputil.UserAgent(internal.UserAgent())
	})

	// Auth
	container.MustRegisterSingleton(auth.NewLoggedInGuard)
	container.MustRegisterSingleton(auth.NewMultiTenantCredentialProvider)
	container.MustRegisterSingleton(func(mgr *auth.Manager) CredentialProviderFn {
		return mgr.CredentialForCurrentUser
	})

	container.MustRegisterSingleton(func(console input.Console) io.Writer {
		writer := console.Handles().Stdout

		if os.Getenv("NO_COLOR") != "" {
			writer = colorable.NewNonColorable(writer)
		}

		return writer
	})

	container.MustRegisterScoped(func(cmd *cobra.Command) internal.EnvFlag {
		// The env flag `-e, --environment` is available on most azd commands but not all
		// This is typically used to override the default environment and is used for bootstrapping other components
		// such as the azd environment.
		// If the flag is not available, don't panic, just return an empty string which will then allow for our default
		// semantics to follow.
		envValue, err := cmd.Flags().GetString(internal.EnvironmentNameFlagName)
		if err != nil {
			log.Printf("'%s'command asked for envFlag, but envFlag was not included in cmd.Flags().", cmd.CommandPath())
			envValue = ""
		}

		return internal.EnvFlag{EnvironmentName: envValue}
	})

	container.MustRegisterSingleton(func(cmd *cobra.Command) CmdAnnotations {
		return cmd.Annotations
	})

	// Azd Context
	container.MustRegisterSingleton(azdcontext.NewAzdContext)

	// Lazy loads the Azd context after the azure.yaml file becomes available
	container.MustRegisterSingleton(func() *lazy.Lazy[*azdcontext.AzdContext] {
		return lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
			return azdcontext.NewAzdContext()
		})
	})

	// Register an initialized environment based on the specified environment flag, or the default environment.
	// Note that referencing an *environment.Environment in a command automatically triggers a UI prompt if the
	// environment is uninitialized or a default environment doesn't yet exist.
	container.MustRegisterScoped(
		func(ctx context.Context,
			azdContext *azdcontext.AzdContext,
			envManager environment.Manager,
			lazyEnv *lazy.Lazy[*environment.Environment],
			envFlags internal.EnvFlag,
		) (*environment.Environment, error) {
			if azdContext == nil {
				return nil, azdcontext.ErrNoProject
			}

			environmentName := envFlags.EnvironmentName
			var err error

			env, err := envManager.LoadOrInitInteractive(ctx, environmentName)
			if err != nil {
				return nil, fmt.Errorf("loading environment: %w", err)
			}

			// Reset lazy env value after loading or creating environment
			// This allows any previous lazy instances (such as hooks) to now point to the same instance
			lazyEnv.SetValue(env)

			return env, nil
		},
	)
	container.MustRegisterScoped(func(lazyEnvManager *lazy.Lazy[environment.Manager]) environment.EnvironmentResolver {
		return func(ctx context.Context) (*environment.Environment, error) {
			azdCtx, err := azdcontext.NewAzdContext()
			if err != nil {
				return nil, err
			}
			defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
			if err != nil {
				return nil, err
			}

			// We need to lazy load the environment manager since it depends on azd context
			envManager, err := lazyEnvManager.GetValue()
			if err != nil {
				return nil, err
			}

			return envManager.Get(ctx, defaultEnv)
		}
	})

	container.MustRegisterSingleton(environment.NewLocalFileDataStore)
	container.MustRegisterSingleton(environment.NewManager)

	container.MustRegisterSingleton(func(serviceLocator ioc.ServiceLocator) *lazy.Lazy[environment.LocalDataStore] {
		return lazy.NewLazy(func() (environment.LocalDataStore, error) {
			var localDataStore environment.LocalDataStore
			err := serviceLocator.Resolve(&localDataStore)
			if err != nil {
				return nil, err
			}

			return localDataStore, nil
		})
	})

	// Environment manager depends on azd context
	container.MustRegisterSingleton(
		func(serviceLocator ioc.ServiceLocator, azdContext *lazy.Lazy[*azdcontext.AzdContext]) *lazy.Lazy[environment.Manager] {
			return lazy.NewLazy(func() (environment.Manager, error) {
				azdCtx, err := azdContext.GetValue()
				if err != nil {
					return nil, err
				}

				// Register the Azd context instance as a singleton in the container if now available
				ioc.RegisterInstance(container, azdCtx)

				var envManager environment.Manager
				err = serviceLocator.Resolve(&envManager)
				if err != nil {
					return nil, err
				}

				return envManager, nil
			})
		},
	)

	container.MustRegisterSingleton(func(
		lazyProjectConfig *lazy.Lazy[*project.ProjectConfig],
		userConfigManager config.UserConfigManager,
	) (*state.RemoteConfig, error) {
		var remoteStateConfig *state.RemoteConfig

		userConfig, err := userConfigManager.Load()
		if err != nil {
			return nil, fmt.Errorf("loading user config: %w", err)
		}

		// The project config may not be available yet
		// Ex) Within init phase of fingerprinting
		projectConfig, _ := lazyProjectConfig.GetValue()

		// Lookup remote state config in the following precedence:
		// 1. Project azure.yaml
		// 2. User configuration
		if projectConfig != nil && projectConfig.State != nil && projectConfig.State.Remote != nil {
			remoteStateConfig = projectConfig.State.Remote
		} else {
			if _, err := userConfig.GetSection("state.remote", &remoteStateConfig); err != nil {
				return nil, fmt.Errorf("getting remote state config: %w", err)
			}
		}

		return remoteStateConfig, nil
	})

	// Lazy loads an existing environment, erroring out if not available
	// One can repeatedly call GetValue to wait until the environment is available.
	container.MustRegisterScoped(
		func(
			ctx context.Context,
			lazyEnvManager *lazy.Lazy[environment.Manager],
			lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
			envFlags internal.EnvFlag,
		) *lazy.Lazy[*environment.Environment] {
			return lazy.NewLazy(func() (*environment.Environment, error) {
				azdCtx, err := lazyAzdContext.GetValue()
				if err != nil {
					return nil, err
				}

				environmentName := envFlags.EnvironmentName
				if environmentName == "" {
					environmentName, err = azdCtx.GetDefaultEnvironmentName()
					if err != nil {
						return nil, err
					}
				}

				envManager, err := lazyEnvManager.GetValue()
				if err != nil {
					return nil, err
				}

				env, err := envManager.Get(ctx, environmentName)
				if err != nil {
					return nil, err
				}

				return env, err
			})
		},
	)

	// Project Config
	// Required to be singleton (shared) because the project/service holds important event handlers
	// from both hooks and internal that are used during azd lifecycle calls.
	container.MustRegisterSingleton(
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
	// Required to be singleton (shared) because the project/service holds important event handlers
	// from both hooks and internal that are used during azd lifecycle calls.
	container.MustRegisterSingleton(
		func(
			serviceLocator ioc.ServiceLocator,
			lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
		) *lazy.Lazy[*project.ProjectConfig] {
			return lazy.NewLazy(func() (*project.ProjectConfig, error) {
				_, err := lazyAzdContext.GetValue()
				if err != nil {
					return nil, err
				}

				var projectConfig *project.ProjectConfig
				err = serviceLocator.Resolve(&projectConfig)

				return projectConfig, err
			})
		},
	)

	container.MustRegisterSingleton(func(
		ctx context.Context,
		credential azcore.TokenCredential,
		httpClient httputil.HttpClient,
	) (*armresourcegraph.Client, error) {
		options := azsdk.
			DefaultClientOptionsBuilder(ctx, httpClient, "azd").
			BuildArmClientOptions()

		return armresourcegraph.NewClient(credential, options)
	})

	container.MustRegisterSingleton(templates.NewTemplateManager)
	container.MustRegisterSingleton(templates.NewSourceManager)
	container.MustRegisterScoped(project.NewResourceManager)
	container.MustRegisterScoped(func(serviceLocator ioc.ServiceLocator) *lazy.Lazy[project.ResourceManager] {
		return lazy.NewLazy(func() (project.ResourceManager, error) {
			var resourceManager project.ResourceManager
			err := serviceLocator.Resolve(&resourceManager)

			return resourceManager, err
		})
	})
	container.MustRegisterSingleton(project.NewProjectManager)
	container.MustRegisterScoped(project.NewDotNetImporter)
	container.MustRegisterScoped(project.NewImportManager)
	container.MustRegisterScoped(project.NewServiceManager)

	// Even though the service manager is scoped based on its use of environment we can still
	// register its internal cache as a singleton to ensure operation caching is consistent across all instances
	container.MustRegisterSingleton(func() project.ServiceOperationCache {
		return project.ServiceOperationCache{}
	})

	container.MustRegisterScoped(func(serviceLocator ioc.ServiceLocator) *lazy.Lazy[project.ServiceManager] {
		return lazy.NewLazy(func() (project.ServiceManager, error) {
			var serviceManager project.ServiceManager
			err := serviceLocator.Resolve(&serviceManager)

			return serviceManager, err
		})
	})
	container.MustRegisterSingleton(repository.NewInitializer)
	container.MustRegisterSingleton(alpha.NewFeaturesManager)
	container.MustRegisterSingleton(config.NewUserConfigManager)
	container.MustRegisterSingleton(config.NewManager)
	container.MustRegisterSingleton(config.NewFileConfigManager)
	container.MustRegisterSingleton(auth.NewManager)
	container.MustRegisterSingleton(azcli.NewUserProfileService)
	container.MustRegisterSingleton(account.NewSubscriptionsService)
	container.MustRegisterSingleton(account.NewManager)
	container.MustRegisterSingleton(account.NewSubscriptionsManager)
	container.MustRegisterSingleton(account.NewSubscriptionCredentialProvider)
	container.MustRegisterSingleton(azcli.NewManagedClustersService)
	container.MustRegisterSingleton(azcli.NewAdService)
	container.MustRegisterSingleton(azcli.NewContainerRegistryService)
	container.MustRegisterSingleton(containerapps.NewContainerAppService)
	container.MustRegisterScoped(project.NewContainerHelper)
	container.MustRegisterSingleton(azcli.NewSpringService)

	container.MustRegisterSingleton(func(subManager *account.SubscriptionsManager) account.SubscriptionTenantResolver {
		return subManager
	})

	container.MustRegisterSingleton(func(ctx context.Context, authManager *auth.Manager) (azcore.TokenCredential, error) {
		return authManager.CredentialForCurrentUser(ctx, nil)
	})

	// Tools
	container.MustRegisterSingleton(func(
		rootOptions *internal.GlobalCommandOptions,
		credentialProvider account.SubscriptionCredentialProvider,
		httpClient httputil.HttpClient,
	) azcli.AzCli {
		return azcli.NewAzCli(credentialProvider, httpClient, azcli.NewAzCliArgs{
			EnableDebug:     rootOptions.EnableDebugLogging,
			EnableTelemetry: rootOptions.EnableTelemetry,
		})
	})
	container.MustRegisterSingleton(azapi.NewDeployments)
	container.MustRegisterSingleton(azapi.NewDeploymentOperations)
	container.MustRegisterSingleton(docker.NewDocker)
	container.MustRegisterSingleton(dotnet.NewDotNetCli)
	container.MustRegisterSingleton(git.NewGitCli)
	container.MustRegisterSingleton(github.NewGitHubCli)
	container.MustRegisterSingleton(javac.NewCli)
	container.MustRegisterSingleton(kubectl.NewKubectl)
	container.MustRegisterSingleton(maven.NewMavenCli)
	container.MustRegisterSingleton(kubelogin.NewCli)
	container.MustRegisterSingleton(helm.NewCli)
	container.MustRegisterSingleton(kustomize.NewCli)
	container.MustRegisterSingleton(npm.NewNpmCli)
	container.MustRegisterSingleton(python.NewPythonCli)
	container.MustRegisterSingleton(swa.NewSwaCli)

	// Provisioning
	container.MustRegisterSingleton(infra.NewAzureResourceManager)
	container.MustRegisterScoped(provisioning.NewManager)
	container.MustRegisterScoped(provisioning.NewPrincipalIdProvider)
	container.MustRegisterScoped(prompt.NewDefaultPrompter)

	// Other
	container.MustRegisterSingleton(createClock)

	// Service Targets
	serviceTargetMap := map[project.ServiceTargetKind]any{
		project.NonSpecifiedTarget:       project.NewAppServiceTarget,
		project.AppServiceTarget:         project.NewAppServiceTarget,
		project.AzureFunctionTarget:      project.NewFunctionAppTarget,
		project.ContainerAppTarget:       project.NewContainerAppTarget,
		project.StaticWebAppTarget:       project.NewStaticWebAppTarget,
		project.AksTarget:                project.NewAksTarget,
		project.SpringAppTarget:          project.NewSpringAppTarget,
		project.DotNetContainerAppTarget: project.NewDotNetContainerAppTarget,
	}

	for target, constructor := range serviceTargetMap {
		container.MustRegisterNamedScoped(string(target), constructor)
	}

	// Languages
	frameworkServiceMap := map[project.ServiceLanguageKind]any{
		project.ServiceLanguageNone:       project.NewNoOpProject,
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
		container.MustRegisterNamedScoped(string(language), constructor)
	}

	container.MustRegisterNamedScoped(string(project.ServiceLanguageDocker), project.NewDockerProjectAsFrameworkService)

	// Pipelines
	container.MustRegisterScoped(pipeline.NewPipelineManager)
	container.MustRegisterSingleton(func(flags *pipelineConfigFlags) *pipeline.PipelineManagerArgs {
		return &flags.PipelineManagerArgs
	})

	pipelineProviderMap := map[string]any{
		"github-ci":  pipeline.NewGitHubCiProvider,
		"github-scm": pipeline.NewGitHubScmProvider,
		"azdo-ci":    pipeline.NewAzdoCiProvider,
		"azdo-scm":   pipeline.NewAzdoScmProvider,
	}

	for provider, constructor := range pipelineProviderMap {
		container.MustRegisterNamedScoped(string(provider), constructor)
	}

	// Platform configuration
	container.MustRegisterSingleton(func(serviceLocator ioc.ServiceLocator) *lazy.Lazy[*platform.Config] {
		return lazy.NewLazy(func() (*platform.Config, error) {
			var platformConfig *platform.Config
			err := serviceLocator.Resolve(&platformConfig)

			return platformConfig, err
		})
	})

	container.MustRegisterSingleton(func(
		lazyProjectConfig *lazy.Lazy[*project.ProjectConfig],
		userConfigManager config.UserConfigManager,
	) (*platform.Config, error) {
		// First check `azure.yaml` for platform configuration section
		projectConfig, err := lazyProjectConfig.GetValue()
		if err == nil && projectConfig != nil && projectConfig.Platform != nil {
			return projectConfig.Platform, nil
		}

		// Fallback to global user configuration
		config, err := userConfigManager.Load()
		if err != nil {
			return nil, fmt.Errorf("loading user config: %w", err)
		}

		var platformConfig *platform.Config
		ok, err := config.GetSection("platform", &platformConfig)
		if err != nil {
			return nil, fmt.Errorf("getting platform config: %w", err)
		}

		if !ok || platformConfig.Type == "" {
			return nil, platform.ErrPlatformConfigNotFound
		}

		// Validate platform type
		supportedPlatformKinds := []string{
			string(devcenter.PlatformKindDevCenter),
			string(azd.PlatformKindDefault),
		}
		if !slices.Contains(supportedPlatformKinds, string(platformConfig.Type)) {
			return nil, fmt.Errorf(
				heredoc.Doc(`platform type '%s' is not supported. Valid values are '%s'.
				Run %s to set or %s to reset. (%w)`),
				platformConfig.Type,
				strings.Join(supportedPlatformKinds, ","),
				output.WithBackticks("azd config set platform.type <type>"),
				output.WithBackticks("azd config unset platform.type"),
				platform.ErrPlatformNotSupported,
			)
		}

		return platformConfig, nil
	})

	// Platform Providers
	platformProviderMap := map[platform.PlatformKind]any{
		azd.PlatformKindDefault:         azd.NewDefaultPlatform,
		devcenter.PlatformKindDevCenter: devcenter.NewPlatform,
	}

	for provider, constructor := range platformProviderMap {
		platformName := fmt.Sprintf("%s-platform", provider)
		container.MustRegisterNamedSingleton(platformName, constructor)
	}

	container.MustRegisterSingleton(func(s ioc.ServiceLocator) (workflow.AzdCommandRunner, error) {
		var rootCmd *cobra.Command
		if err := s.ResolveNamed("root-cmd", &rootCmd); err != nil {
			return nil, err
		}
		return &workflowCmdAdapter{cmd: rootCmd}, nil

	})
	container.MustRegisterSingleton(workflow.NewRunner)

	// Required for nested actions called from composite actions like 'up'
	registerAction[*cmd.ProvisionAction](container, "azd-provision-action")
	registerAction[*downAction](container, "azd-down-action")
	registerAction[*configShowAction](container, "azd-config-show-action")
}

// workflowCmdAdapter adapts a cobra command to the workflow.AzdCommandRunner interface
type workflowCmdAdapter struct {
	cmd *cobra.Command
}

func (w *workflowCmdAdapter) SetArgs(args []string) {
	w.cmd.SetArgs(args)
}

// ExecuteContext implements workflow.AzdCommandRunner
func (w *workflowCmdAdapter) ExecuteContext(ctx context.Context) error {
	childCtx := middleware.WithChildAction(ctx)
	return w.cmd.ExecuteContext(childCtx)
}
