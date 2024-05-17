package devcenter

import (
	"context"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/state"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
)

// Platform manages the Azd configuration of the devcenter platform
type Platform struct {
	config *platform.Config
}

func NewPlatform(config *platform.Config) platform.Provider {
	return &Platform{
		config: config,
	}
}

// Name returns the name of the platform
func (p *Platform) Name() string {
	return "devcenter"
}

// IsEnabled returns true if the devcenter platform is enabled
func (p *Platform) IsEnabled() bool {
	return p.config.Type == PlatformKindDevCenter
}

// ConfigureContainer configures the IoC container for the devcenter platform components
func (p *Platform) ConfigureContainer(container *ioc.NestedContainer) error {
	// DevCenter Config
	container.MustRegisterTransient(func(
		ctx context.Context,
		lazyAzdCtx *lazy.Lazy[*azdcontext.AzdContext],
		userConfigManager config.UserConfigManager,
		lazyProjectConfig *lazy.Lazy[*project.ProjectConfig],
		lazyLocalEnvStore *lazy.Lazy[environment.LocalDataStore],
	) (*Config, error) {
		// Load deventer configuration in the following precedence:
		// 1. Environment variables (AZURE_DEVCENTER_*)
		// 2. Azd Environment configuration (devCenter node)
		// 3. Azd Project configuration from azure.yaml (devCenter node)
		// 4. Azd user configuration from config.json (devCenter node)

		// Shell environment variables
		envVarConfig := &Config{
			Name:                  os.Getenv(DevCenterNameEnvName),
			Project:               os.Getenv(DevCenterProjectEnvName),
			Catalog:               os.Getenv(DevCenterCatalogEnvName),
			EnvironmentType:       os.Getenv(DevCenterEnvTypeEnvName),
			EnvironmentDefinition: os.Getenv(DevCenterEnvDefinitionEnvName),
			User:                  os.Getenv(DevCenterEnvUser),
		}

		azdCtx, _ := lazyAzdCtx.GetValue()
		localEnvStore, _ := lazyLocalEnvStore.GetValue()

		// Local environment configuration
		var environmentConfig *Config
		if azdCtx != nil && localEnvStore != nil {
			defaultEnvName, err := azdCtx.GetDefaultEnvironmentName()
			if err != nil {
				environmentConfig = &Config{}
			} else {
				// Attempt to load any devcenter configuration from local environment
				env, err := localEnvStore.Get(ctx, defaultEnvName)
				if err == nil {
					devCenterNode, exists := env.Config.Get(ConfigPath)
					if exists {
						value, err := ParseConfig(devCenterNode)
						if err != nil {
							return nil, err
						}

						environmentConfig = value
					}
				}
			}
		}

		// User Configuration
		var userConfig *Config
		azdConfig, err := userConfigManager.Load()
		if err != nil {
			userConfig = &Config{}
		} else {
			devCenterNode, exists := azdConfig.Get(ConfigPath)
			if exists {
				value, err := ParseConfig(devCenterNode)
				if err != nil {
					return nil, err
				}

				userConfig = value
			}
		}

		// Project Configuration
		var projectConfig *Config
		projConfig, _ := lazyProjectConfig.GetValue()
		if projConfig != nil && projConfig.Platform != nil {
			value, err := ParseConfig(projConfig.Platform.Config)
			if err == nil {
				projectConfig = value
			}
		}

		return MergeConfigs(
			envVarConfig,
			environmentConfig,
			projectConfig,
			userConfig,
		), nil
	})

	// Override default provision provider
	container.MustRegisterSingleton(func() provisioning.DefaultProviderResolver {
		return func() (provisioning.ProviderKind, error) {
			return ProvisionKindDevCenter, nil
		}
	})

	// Override default template sources
	container.MustRegisterSingleton(func() *templates.SourceOptions {
		return &templates.SourceOptions{
			DefaultSources:        []*templates.SourceConfig{SourceDevCenter},
			LoadConfiguredSources: false,
		}
	})

	// Configure remote environment storage
	container.MustRegisterSingleton(func() *state.RemoteConfig {
		return &state.RemoteConfig{
			Backend: string(RemoteKindDevCenter),
		}
	})

	// Provision Provider
	container.MustRegisterNamedTransient(string(ProvisionKindDevCenter), NewProvisionProvider)

	// Remote Environment Storage
	container.MustRegisterNamedTransient(string(RemoteKindDevCenter), NewEnvironmentStore)

	// Template Sources
	container.MustRegisterNamedTransient(string(SourceKindDevCenter), NewTemplateSource)

	container.MustRegisterSingleton(NewManager)
	container.MustRegisterSingleton(NewPrompter)

	// Other devcenter components
	container.MustRegisterSingleton(func(
		credentialProvider auth.MultiTenantCredentialProvider,
		policyClientOptions *azcore.ClientOptions,
		armClientOptions *arm.ClientOptions,
		cloud *cloud.Cloud,
	) (devcentersdk.DevCenterClient, error) {
		// Use home tenant ID
		cred, err := credentialProvider.GetTokenCredential(context.Background(), "")
		if err != nil {
			return nil, err
		}

		resourceGraphClient, err := armresourcegraph.NewClient(cred, armClientOptions)
		if err != nil {
			return nil, err
		}

		return devcentersdk.NewDevCenterClient(cred, policyClientOptions, resourceGraphClient, cloud)
	})

	return nil
}
