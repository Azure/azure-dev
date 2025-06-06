// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azd

import (
	"encoding/json"
	"fmt"
	"strings"
	"log"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/cosmosdb"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	infraBicep "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	infraTerraform "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/terraform"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/sqldb"
	"github.com/azure/azure-dev/cli/azd/pkg/state"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/terraform"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/typescript"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
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
	container.MustRegisterSingleton(terraform.NewCli)
	container.MustRegisterSingleton(bicep.NewCli)

	container.MustRegisterTransient(func() *lazy.Lazy[*infraBicep.BicepProvider] {
		return lazy.NewLazy(func() (*infraBicep.BicepProvider, error) {
			var provider provisioning.Provider
			if err := container.ResolveNamed(string(provisioning.Bicep), &provider); err != nil {
				return nil, err
			}

			bicepProvider, ok := provider.(*infraBicep.BicepProvider)
			if !ok {
				return nil, fmt.Errorf("unexpected provider type: %T", provider)
			}

			return bicepProvider, nil
		})
	})

	container.MustRegisterTransient(
		func(lazyBicepProvider *lazy.Lazy[*infraBicep.BicepProvider],
		) (*infraBicep.BicepProvider, error) {
			return lazyBicepProvider.GetValue()
		})

	container.MustRegisterTransient(infraBicep.NewBicepProvider)

	// Provisioning Providers

   // Register Bicep and Terraform providers as before
   container.MustRegisterNamedTransient(string(provisioning.Bicep), infraBicep.NewBicepProvider)
   container.MustRegisterNamedTransient(string(provisioning.Terraform), infraTerraform.NewTerraformProvider)

   // Register TypeScript provider with explicit dependency resolution, using lazy envManager/env
container.MustRegisterNamedTransient(string(provisioning.TypeScript), func(serviceLocator ioc.ServiceLocator) (provisioning.Provider, error) {
    var envManager environment.Manager
    var env *environment.Environment
    var console input.Console
    var prompters prompt.Prompter
    if err := serviceLocator.Resolve(&envManager); err != nil {
        return nil, err
    }
    if err := serviceLocator.Resolve(&env); err != nil {
        return nil, err
    }
    if err := serviceLocator.Resolve(&console); err != nil {
        return nil, err
    }
    if err := serviceLocator.Resolve(&prompters); err != nil {
        return nil, err
    }
    log.Printf("Resolved envManager: %#v", envManager)
    log.Printf("Resolved env: %#v", env)
    return typescript.NewTypeScriptProvider(envManager, env, console, prompters), nil
})

	// Function to determine the default IaC provider when provisioning
	container.MustRegisterSingleton(func() provisioning.DefaultProviderResolver {
		return func() (provisioning.ProviderKind, error) {
			return provisioning.Bicep, nil
		}
	})

	// Remote Environment State Providers
	remoteStateProviderMap := map[environment.RemoteKind]any{
		environment.RemoteKindAzureBlobStorage: environment.NewStorageBlobDataStore,
	}

	for remoteKind, constructor := range remoteStateProviderMap {
		container.MustRegisterNamedScoped(string(remoteKind), constructor)
	}

	container.MustRegisterSingleton(func(
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
	container.MustRegisterSingleton(storage.NewBlobClient)
	container.MustRegisterSingleton(storage.NewBlobSdkClient)

	// cosmosdb
	container.MustRegisterSingleton(cosmosdb.NewCosmosDbService)

	// sqldb
	container.MustRegisterSingleton(sqldb.NewSqlDbService)

	// Templates

	// Gets a list of default template sources used in azd.
	container.MustRegisterSingleton(func() *templates.SourceOptions {
		return &templates.SourceOptions{
			DefaultSources:        []*templates.SourceConfig{},
			LoadConfiguredSources: true,
		}
	})


   return nil
}
