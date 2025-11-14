// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"

// InfraConfig represents infrastructure configuration from azure.yaml
// This is a data-only representation that can be converted to provisioning.Options
type InfraConfig struct {
	Provider         string         `yaml:"provider,omitempty"`
	Path             string         `yaml:"path,omitempty"`
	Module           string         `yaml:"module,omitempty"`
	Name             string         `yaml:"name,omitempty"`
	Layers           []InfraConfig  `yaml:"layers,omitempty"`
	DeploymentStacks map[string]any `yaml:"deploymentStacks,omitempty"`
}

// ToProvisioningOptions converts InfraConfig to provisioning.Options
func (ic *InfraConfig) ToProvisioningOptions() provisioning.Options {
	layers := make([]provisioning.Options, len(ic.Layers))
	for i, layer := range ic.Layers {
		layers[i] = layer.ToProvisioningOptions()
	}

	return provisioning.Options{
		Provider:         provisioning.ProviderKind(ic.Provider),
		Path:             ic.Path,
		Module:           ic.Module,
		Name:             ic.Name,
		DeploymentStacks: ic.DeploymentStacks,
		Layers:           layers,
	}
}

// InfraConfigFromProvisioningOptions creates InfraConfig from provisioning.Options
func InfraConfigFromProvisioningOptions(opts provisioning.Options) InfraConfig {
	layers := make([]InfraConfig, len(opts.Layers))
	for i, layer := range opts.Layers {
		layers[i] = InfraConfigFromProvisioningOptions(layer)
	}

	return InfraConfig{
		Provider:         string(opts.Provider),
		Path:             opts.Path,
		Module:           opts.Module,
		Name:             opts.Name,
		DeploymentStacks: opts.DeploymentStacks,
		Layers:           layers,
	}
}
