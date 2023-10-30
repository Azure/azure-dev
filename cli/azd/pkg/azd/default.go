package azd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	infraBicep "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/cdk"
	infraTerraform "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/terraform"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/state"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/terraform"
)

const PlatformKindDefault platform.PlatformKind = "default"

// DefaultPlatform manages the Azd configuration of the default platform
type DefaultPlatform struct {
}

// NewDefaultPlatform creates a new instance of the default platform
func NewDefaultPlatform() platform.Provider {
	return &DefaultPlatform{}
}

// Name returns the name of the platform
func (p *DefaultPlatform) Name() string {
	return "default"
}

// IsEnabled returns true when the platform is enabled
func (p *DefaultPlatform) IsEnabled() bool {
	return true
}

// ConfigureContainer configures the IoC container for the default platform components
func (p *DefaultPlatform) ConfigureContainer(container *ioc.NestedContainer) error {
	// Tools
	container.RegisterSingleton(terraform.NewTerraformCli)
	container.RegisterSingleton(bicep.NewBicepCli)

	// Provisioning Providers
	provisionProviderMap := map[provisioning.ProviderKind]any{
		provisioning.Bicep:     infraBicep.NewBicepProvider,
		provisioning.Terraform: infraTerraform.NewTerraformProvider,
		provisioning.Cdk:       cdk.NewCdkProvider,
	}

	for provider, constructor := range provisionProviderMap {
		if err := container.RegisterNamedTransient(string(provider), constructor); err != nil {
			panic(fmt.Errorf("registering IaC provider %s: %w", provider, err))
		}
	}

	// Function to determine the default IaC provider when provisioning
	container.RegisterSingleton(func() provisioning.DefaultProviderResolver {
		return func() (provisioning.ProviderKind, error) {
			return provisioning.Bicep, nil
		}
	})

	// Remote Environment State Providers
	remoteStateProviderMap := map[environment.RemoteKind]any{
		environment.RemoteKindAzureBlobStorage: environment.NewStorageBlobDataStore,
	}

	for remoteKind, constructor := range remoteStateProviderMap {
		if err := container.RegisterNamedSingleton(string(remoteKind), constructor); err != nil {
			panic(fmt.Errorf("registering remote state provider %s: %w", remoteKind, err))
		}
	}

	container.RegisterSingleton(func(
		remoteStateConfig *state.RemoteConfig,
		projectConfig *project.ProjectConfig,
	) (*storage.AccountConfig, error) {
		if remoteStateConfig == nil {
			return nil, nil
		}

		var storageAccountConfig *storage.AccountConfig
		jsonBytes, err := json.Marshal(remoteStateConfig.Config)
		if err != nil {
			return nil, fmt.Errorf("marshalling remote state config: %w", err)
		}

		if err := json.Unmarshal(jsonBytes, &storageAccountConfig); err != nil {
			return nil, fmt.Errorf("unmarshalling remote state config: %w", err)
		}

		// If a container name has not been explicitly configured
		// Default to use the project name as the container name
		if storageAccountConfig.ContainerName == "" {
			// Azure blob storage containers must be lowercase and can only container alphanumeric characters and hyphens
			// We will do our best to preserve the original project name by forcing to lowercase.
			storageAccountConfig.ContainerName = strings.ToLower(projectConfig.Name)
		}

		return storageAccountConfig, nil
	})

	// Storage components
	container.RegisterSingleton(storage.NewBlobClient)
	container.RegisterSingleton(storage.NewBlobSdkClient)

	// Templates

	// Gets a list of default template sources used in azd.
	container.RegisterSingleton(func() *templates.SourceOptions {
		return &templates.SourceOptions{
			DefaultSources:        []*templates.SourceConfig{},
			LoadConfiguredSources: true,
		}
	})

	return nil
}
