// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"

// ProjectFormat identifies the persisted project layout.
type ProjectFormat int

const (
	// ProjectFormatFlat is when you have `services:` and a single `infra:` entry at the top level:
	//
	// services:
	//   <services>
	// infra:
	//   provider: bicep
	ProjectFormatFlat ProjectFormat = iota

	// ProjectFormatInfraV1 is when you have `services:` at the top level, similar to the flat infra,
	// but you have layers in infra:
	//
	// services:
	//   <services>
	// infra:
	//    layers:
	//    - name: layer-a
	//      provider: bicep
	//    - name: layer-b
	//      provider: bicep
	ProjectFormatInfraV1

	// ProjectFormatLayersV2 is when you have a top-level `layers:` entry, where each layer
	// can have `infra` and `services`:
	//
	// layers:
	// - name: layer-a
	//   infra:
	//   - name: infra-a
	//     provider: bicep
	//   services:
	//     <services>
	// - name: layer-b
	//   services:
	//     <services>
	ProjectFormatLayersV2
)

// LayerConfig is one persisted project layer in azure.yaml.
type LayerConfig struct {
	Name     string                    `yaml:"name"`
	Infra    []provisioning.Options    `yaml:"infra,omitempty"`
	Services map[string]*ServiceConfig `yaml:"services,omitempty"`
}

// LayerConfigs preserves an explicitly empty layers collection when marshaled.
type LayerConfigs []*LayerConfig

// IsZero reports whether the layers field was absent from the project configuration.
func (layers LayerConfigs) IsZero() bool {
	// we only want to omit the 'layers' field if it's non-existent. If it's
	// just empty we're still a layers based project, just without any layers.
	return layers == nil
}

// Format returns the persisted layout used by the project.
func (pc *ProjectConfig) Format() ProjectFormat {
	if pc.Layers != nil {
		return ProjectFormatLayersV2
	}
	if pc.Infra.Layers != nil {
		return ProjectFormatInfraV1
	}
	return ProjectFormatFlat
}

// ServiceConfigs returns all configured services keyed by their globally unique names.
func (pc *ProjectConfig) ServiceConfigs() map[string]*ServiceConfig {
	if pc.Format() != ProjectFormatLayersV2 {
		return pc.Services
	}

	services := make(map[string]*ServiceConfig)
	for _, layer := range pc.Layers {
		for name, service := range layer.Services {
			services[name] = service
		}
	}
	return services
}

// InfrastructureConfigs returns all provisioning entries in declaration order.
func (pc *ProjectConfig) InfrastructureConfigs() []provisioning.Options {
	if pc.Format() != ProjectFormatLayersV2 {
		return pc.Infra.GetLayers()
	}

	var entries []provisioning.Options
	for _, layer := range pc.Layers {
		for _, infra := range layer.Infra {
			infra.Layer = layer.Name
			entries = append(entries, infra)
		}
	}
	return entries
}
