package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/internal/repository"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azd"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/containerapps"
	"github.com/azure/azure-dev/cli/azd/pkg/containerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/devcenter"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/helm"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
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

		return input.NewConsole(rootOptions.NoPrompt, isTerminal, input.Writers{Output: writer}, input.ConsoleHandles{
			Stdin:  cmd.InOrStdin(),
			Stdout: cmd.OutOrStdout(),
			Stderr: cmd.ErrOrStderr(),
		}, formatter, nil)
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
	ioc.RegisterInstance[policy.Transporter](container, client)
	ioc.RegisterInstance[auth.HttpClient](container, client)

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
	container.MustRegisterSingleton(func(lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]) (*azdcontext.AzdContext, error) {
		return lazyAzdContext.GetValue()
	})

	// Lazy loads the Azd context after the azure.yaml file becomes available
	container.MustRegisterSingleton(func() *lazy.Lazy[*azdcontext.AzdContext] {
		return lazy.NewLazy(azdcontext.NewAzdContext)
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
		func(serviceLocator ioc.ServiceLocator,
			azdContext *lazy.Lazy[*azdcontext.AzdContext]) *lazy.Lazy[environment.Manager] {
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
	container.MustRegisterScoped(
		func(lazyConfig *lazy.Lazy[*project.ProjectConfig]) (*project.ProjectConfig, error) {
			return lazyConfig.GetValue()
		},
	)

	// Lazy loads the project config from the Azd Context when it becomes available
	container.MustRegisterScoped(
		func(
			ctx context.Context,
			lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
		) *lazy.Lazy[*project.ProjectConfig] {
			return lazy.NewLazy(func() (*project.ProjectConfig, error) {
				azdCtx, err := lazyAzdContext.GetValue()
				if err != nil {
					return nil, err
				}

				projectConfig, err := project.Load(ctx, azdCtx.ProjectPath())
				if err != nil {
					return nil, err
				}

				return projectConfig, nil
			})
		},
	)

	container.MustRegisterSingleton(func(
		ctx context.Context,
		userConfigManager config.UserConfigManager,
		lazyProjectConfig *lazy.Lazy[*project.ProjectConfig],
		lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
		lazyLocalEnvStore *lazy.Lazy[environment.LocalDataStore],
	) (*cloud.Cloud, error) {

		// Precedence for cloud configuration:
		// 1. Local environment config (.azure/<environment>/config.json)
		// 2. Project config (azure.yaml)
		// 3. User config (~/.azure/config.json)
		// Default if no cloud configured: Azure Public Cloud

		validClouds := fmt.Sprintf(
			"Valid cloud names are '%s', '%s', '%s'.",
			cloud.AzurePublicName,
			cloud.AzureChinaCloudName,
			cloud.AzureUSGovernmentName,
		)

		// Local Environment Configuration (.azure/<environment>/config.json)
		localEnvStore, _ := lazyLocalEnvStore.GetValue()
		if azdCtx, err := lazyAzdContext.GetValue(); err == nil {
			if azdCtx != nil && localEnvStore != nil {
				if defaultEnvName, err := azdCtx.GetDefaultEnvironmentName(); err == nil {
					if env, err := localEnvStore.Get(ctx, defaultEnvName); err == nil {
						if cloudConfigurationNode, exists := env.Config.Get(cloud.ConfigPath); exists {
							if value, err := cloud.ParseCloudConfig(cloudConfigurationNode); err == nil {
								cloudConfig, err := cloud.NewCloud(value)
								if err == nil {
									return cloudConfig, nil
								}

								return nil, &internal.ErrorWithSuggestion{
									Err: err,
									Suggestion: fmt.Sprintf(
										"Set the cloud configuration by editing the 'cloud' node in the config.json "+
											"file for the %s environment\n%s",
										defaultEnvName,
										validClouds,
									),
								}
							}
						}
					}
				}
			}
		}

		// Project Configuration (azure.yaml)
		projConfig, err := lazyProjectConfig.GetValue()
		if err == nil && projConfig != nil && projConfig.Cloud != nil {
			if value, err := cloud.ParseCloudConfig(projConfig.Cloud); err == nil {
				if cloudConfig, err := cloud.ParseCloudConfig(value); err == nil {
					if cloud, err := cloud.NewCloud(cloudConfig); err == nil {
						return cloud, nil
					} else {
						return nil, &internal.ErrorWithSuggestion{
							Err: err,
							//nolint:lll
							Suggestion: fmt.Sprintf("Set the cloud configuration by editing the 'cloud' node in the project YAML file\n%s", validClouds),
						}
					}
				}
			}
		}

		// User Configuration (~/.azure/config.json)
		if azdConfig, err := userConfigManager.Load(); err == nil {
			if cloudConfigNode, exists := azdConfig.Get(cloud.ConfigPath); exists {
				if value, err := cloud.ParseCloudConfig(cloudConfigNode); err == nil {
					if cloud, err := cloud.NewCloud(value); err == nil {
						return cloud, nil
					} else {
						return nil, &internal.ErrorWithSuggestion{
							Err: err,
							Suggestion: fmt.Sprintf(
								"Set the cloud configuration using 'azd config set cloud.name <name>'.\n%s", validClouds),
						}
					}
				}
			}
		}

		return cloud.NewCloud(&cloud.Config{Name: cloud.AzurePublicName})
	})

	container.MustRegisterSingleton(func(transport policy.Transporter, cloud *cloud.Cloud) *azcore.ClientOptions {
		return &azcore.ClientOptions{
			Cloud: cloud.Configuration,
			PerCallPolicies: []policy.Policy{
				azsdk.NewMsCorrelationPolicy(),
				azsdk.NewUserAgentPolicy(internal.UserAgent()),
			},
			Transport: transport,
		}
	})

	container.MustRegisterSingleton(func(transport policy.Transporter, cloud *cloud.Cloud) *arm.ClientOptions {
		return &arm.ClientOptions{
			ClientOptions: azcore.ClientOptions{
				Cloud: cloud.Configuration,
				Logging: policy.LogOptions{
					AllowedHeaders: []string{azsdk.MsCorrelationIdHeader},
				},
				PerCallPolicies: []policy.Policy{
					azsdk.NewMsCorrelationPolicy(),
					azsdk.NewUserAgentPolicy(internal.UserAgent()),
				},
				Transport: transport,
			},
		}
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
	container.MustRegisterScoped(project.NewProjectManager)
	// Currently caches manifest across command executions
	container.MustRegisterSingleton(project.NewDotNetImporter)
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
	container.MustRegisterScoped(func() (auth.ExternalAuthConfiguration, error) {
		cert := os.Getenv("AZD_AUTH_CERT")
		endpoint := os.Getenv("AZD_AUTH_ENDPOINT")
		key := os.Getenv("AZD_AUTH_KEY")

		client := &http.Client{}
		if len(cert) > 0 {
			transport, err := httputil.TlsEnabledTransport(cert)
			if err != nil {
				return auth.ExternalAuthConfiguration{},
					fmt.Errorf("parsing AZD_AUTH_CERT: %w", err)
			}
			client.Transport = transport

			endpointUrl, err := url.Parse(endpoint)
			if err != nil {
				return auth.ExternalAuthConfiguration{},
					fmt.Errorf("invalid AZD_AUTH_ENDPOINT value '%s': %w", endpoint, err)
			}

			if endpointUrl.Scheme != "https" {
				return auth.ExternalAuthConfiguration{},
					fmt.Errorf("invalid AZD_AUTH_ENDPOINT value '%s': scheme must be 'https' when certificate is provided",
						endpoint)
			}
		}
		return auth.ExternalAuthConfiguration{
			Endpoint:    endpoint,
			Transporter: client,
			Key:         key,
		}, nil
	})
	container.MustRegisterScoped(auth.NewManager)
	container.MustRegisterSingleton(azapi.NewUserProfileService)
	container.MustRegisterSingleton(account.NewSubscriptionsService)
	container.MustRegisterSingleton(account.NewManager)
	container.MustRegisterSingleton(account.NewSubscriptionsManager)
	container.MustRegisterSingleton(account.NewSubscriptionCredentialProvider)
	container.MustRegisterSingleton(azapi.NewManagedClustersService)
	container.MustRegisterSingleton(entraid.NewEntraIdService)
	container.MustRegisterSingleton(azapi.NewContainerRegistryService)
	container.MustRegisterSingleton(containerapps.NewContainerAppService)
	container.MustRegisterSingleton(containerregistry.NewRemoteBuildManager)
	container.MustRegisterSingleton(keyvault.NewKeyVaultService)
	container.MustRegisterSingleton(storage.NewFileShareService)
	container.MustRegisterScoped(project.NewContainerHelper)
	container.MustRegisterSingleton(azapi.NewSpringService)

	container.MustRegisterSingleton(func(subManager *account.SubscriptionsManager) account.SubscriptionTenantResolver {
		return subManager
	})

	// Tools
	container.MustRegisterSingleton(azapi.NewAzureClient)

	// Tools
	container.MustRegisterSingleton(azapi.NewResourceService)
	container.MustRegisterSingleton(docker.NewCli)
	container.MustRegisterSingleton(dotnet.NewCli)
	container.MustRegisterSingleton(git.NewCli)
	container.MustRegisterSingleton(github.NewGitHubCli)
	container.MustRegisterSingleton(javac.NewCli)
	container.MustRegisterSingleton(kubectl.NewCli)
	container.MustRegisterSingleton(maven.NewCli)
	container.MustRegisterSingleton(kubelogin.NewCli)
	container.MustRegisterSingleton(helm.NewCli)
	container.MustRegisterSingleton(kustomize.NewCli)
	container.MustRegisterSingleton(npm.NewCli)
	container.MustRegisterSingleton(python.NewCli)
	container.MustRegisterSingleton(swa.NewCli)
	container.MustRegisterScoped(ai.NewPythonBridge)
	container.MustRegisterScoped(project.NewAiHelper)

	// Provisioning
	container.MustRegisterSingleton(func(
		serviceLocator ioc.ServiceLocator,
		featureManager *alpha.FeatureManager,
	) (azapi.DeploymentService, error) {
		deploymentsType := azapi.DeploymentTypeStandard

		if featureManager.IsEnabled(azapi.FeatureDeploymentStacks) {
			deploymentsType = azapi.DeploymentTypeStacks
		}

		var deployments azapi.DeploymentService
		if err := serviceLocator.ResolveNamed(string(deploymentsType), &deployments); err != nil {
			return nil, err
		}

		return deployments, nil
	})

	container.MustRegisterSingleton(azapi.NewResourceService)

	// Register Deployment Services
	deploymentServiceTypes := map[azapi.DeploymentType]any{
		azapi.DeploymentTypeStandard: func(deploymentService *azapi.StandardDeployments) azapi.DeploymentService {
			return deploymentService
		},
		azapi.DeploymentTypeStacks: func(deploymentService *azapi.StackDeployments) azapi.DeploymentService {
			return deploymentService
		},
	}

	for deploymentType, constructor := range deploymentServiceTypes {
		container.MustRegisterNamedSingleton(string(deploymentType), constructor)
	}

	container.MustRegisterSingleton(azapi.NewStandardDeployments)
	container.MustRegisterSingleton(azapi.NewStackDeployments)
	container.MustRegisterScoped(infra.NewDeploymentManager)
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
		project.AiEndpointTarget:         project.NewAiEndpointTarget,
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
		project.ServiceLanguageSwa:        project.NewSwaProject,
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
	container.MustRegisterSingleton(func(lazyConfig *lazy.Lazy[*platform.Config]) (*platform.Config, error) {
		return lazyConfig.GetValue()
	})

	container.MustRegisterSingleton(func(
		lazyProjectConfig *lazy.Lazy[*project.ProjectConfig],
		userConfigManager config.UserConfigManager,
	) *lazy.Lazy[*platform.Config] {
		return lazy.NewLazy(func() (*platform.Config, error) {
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
			_, err = config.GetSection("platform", &platformConfig)
			if err != nil {
				return nil, fmt.Errorf("getting platform config: %w", err)
			}

			// If we still don't have a platform configuration, check the OS environment
			// We check the OS environment instead of AZD environment because the global platform configuration
			// cannot be known at this time in the azd bootstrapping process.
			if platformConfig == nil {
				if envPlatformType, has := os.LookupEnv(environment.PlatformTypeEnvVarName); has {
					platformConfig = &platform.Config{
						Type: platform.PlatformKind(envPlatformType),
					}
				}
			}

			if platformConfig == nil || platformConfig.Type == "" {
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

// ArmClientInitializer is a function definition for all Azure SDK ARM Client
type ArmClientInitializer[T comparable] func(
	subscriptionId string,
	credentials azcore.TokenCredential,
	armClientOptions *arm.ClientOptions,
) (T, error)
