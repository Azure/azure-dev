// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azd

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/cosmosdb"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
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
	provisionProviderMap := map[provisioning.ProviderKind]any{
		provisioning.Bicep:     infraBicep.NewBicepProvider,
		provisioning.Terraform: infraTerraform.NewTerraformProvider,
	}

	for provider, constructor := range provisionProviderMap {
		container.MustRegisterNamedTransient(string(provider), constructor)
	}

	// DefaultProviderResolver picks the IaC provider when azure.yaml doesn't set
	// infra.provider. If the project has no on-disk IaC but uses service hosts owned by an
	// installed extension that also ships a provisioning provider, route to that provider
	// (e.g. a Foundry azure.yaml -> microsoft.foundry, no infra/ directory needed).
	// Otherwise default to bicep.
	container.MustRegisterSingleton(func() provisioning.DefaultProviderResolver {
		return func() (provisioning.ProviderKind, error) {
			if provider, ok := defaultProviderFromExtensions(container); ok {
				return provider, nil
			}

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

// defaultProviderFromExtensions returns the provisioning provider an installed extension
// supplies for the loaded project's service hosts. It returns false (so the caller falls
// back to bicep) when there's no project, no extension manager, or no matching extension.
func defaultProviderFromExtensions(serviceLocator ioc.ServiceLocator) (provisioning.ProviderKind, bool) {
	var projectConfig *project.ProjectConfig
	if err := serviceLocator.Resolve(&projectConfig); err != nil || projectConfig == nil {
		return "", false
	}

	var extensionManager *extensions.Manager
	if err := serviceLocator.Resolve(&extensionManager); err != nil || extensionManager == nil {
		return "", false
	}

	installed, err := extensionManager.ListInstalled()
	if err != nil {
		return "", false
	}

	return providerFromInstalledExtensions(projectConfig.Services, installed)
}

// providerFromInstalledExtensions finds an installed extension that both registers a
// provisioning provider and serves one of the project's service hosts, and returns that
// provisioning provider (for example, the Foundry extension serves azure.ai.project and
// registers microsoft.foundry). It returns false when nothing matches. Extension ids are
// sorted so the choice is deterministic when more than one extension qualifies.
func providerFromInstalledExtensions(
	services map[string]*project.ServiceConfig,
	installed map[string]*extensions.Extension,
) (provisioning.ProviderKind, bool) {
	if len(services) == 0 || len(installed) == 0 {
		return "", false
	}

	hosts := make(map[string]struct{}, len(services))
	for _, svc := range services {
		if svc != nil && svc.Host != "" {
			hosts[string(svc.Host)] = struct{}{}
		}
	}

	if len(hosts) == 0 {
		return "", false
	}

	ids := make([]string, 0, len(installed))
	for id := range installed {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	for _, id := range ids {
		ext := installed[id]
		if ext == nil || !ext.HasCapability(extensions.ProvisioningProviderCapability) {
			continue
		}

		var provisioningProvider string
		servesHost := false
		for _, provider := range ext.Providers {
			switch provider.Type {
			case extensions.ProvisioningProviderType:
				if provisioningProvider == "" {
					provisioningProvider = provider.Name
				}
			case extensions.ServiceTargetProviderType:
				if _, ok := hosts[provider.Name]; ok {
					servesHost = true
				}
			}
		}

		if provisioningProvider != "" && servesHost {
			return provisioning.ProviderKind(provisioningProvider), true
		}
	}

	return "", false
}
