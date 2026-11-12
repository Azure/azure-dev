// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/require"
)

func TestExtensionProviderConfigurationIsOpaque(t *testing.T) {
	for _, test := range []struct {
		name string
		yaml string
	}{
		{name: "flat", yaml: `name: example
infra:
  provider: custom
  config:
    private: null
    settings: [true, custom]
`},
		{name: "infra layers", yaml: `name: example
infra:
  layers:
    - name: first
      provider: custom
      config:
        private: null
        settings: [true, custom]
    - name: second
      provider: custom
      config:
        private: null
        settings: [true, custom]
`},
		{name: "project layers", yaml: `name: example
layers:
  - name: application
    infra:
      - name: first
        provider: custom
        config:
          private: null
          settings: [true, custom]
`},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := Parse(t.Context(), test.yaml)
			require.NoError(t, err)
			require.NoError(t, config.Validate())
			require.Empty(t, config.Resources)
			for _, entry := range config.InfrastructureConfigs() {
				require.Equal(t, map[string]any{
					"private": nil, "settings": []any{true, "custom"},
				}, entry.Config)
			}

			encoded, err := yaml.Marshal(config)
			require.NoError(t, err)
			reloaded, err := Parse(t.Context(), string(encoded))
			require.NoError(t, err)
			require.Equal(t, config.InfrastructureConfigs(), reloaded.InfrastructureConfigs())
		})
	}
}

func TestExtensionProviderDoesNotUseLegacyResourceFallback(t *testing.T) {
	config := &ProjectConfig{
		Path: t.TempDir(),
		Infra: provisioning.Options{
			Provider: "custom",
			Config:   map[string]any{"private": "configuration"},
		},
		Resources: map[string]*ResourceConfig{
			"cache": {Name: "cache", Type: ResourceTypeDbRedis},
		},
	}
	infra, err := NewImportManager(nil).ProjectInfrastructure(t.Context(), config)
	require.NoError(t, err)
	require.Equal(t, provisioning.ProviderKind("custom"), infra.Options.Provider)
	require.Equal(t, config.Infra.Config, infra.Options.Config)
	require.Empty(t, infra.cleanupDir)
	require.False(t, infra.IsCompose)
}

func TestGenerateInfrastructureRejectsExtensionProviders(t *testing.T) {
	for _, infra := range []provisioning.Options{
		{Provider: "custom", Config: map[string]any{"private": true}},
		{Layers: []provisioning.Options{
			{Name: "native", Provider: provisioning.Bicep, Path: "infra/native"},
			{Name: "extension", Provider: "custom"},
		}},
	} {
		config := &ProjectConfig{
			Infra: infra,
			Resources: map[string]*ResourceConfig{
				"cache": {Name: "cache", Type: ResourceTypeDbRedis},
			},
		}
		files, err := NewImportManager(nil).GenerateAllInfrastructure(t.Context(), config)
		require.Nil(t, files)
		require.ErrorContains(t, err, `generation is not supported for extension provider "custom"`)
	}
}

func TestGenerateInfrastructureRejectsProjectLayerExtension(t *testing.T) {
	config := &ProjectConfig{
		Layers: LayerConfigs{{
			Name: "application",
			Infra: []provisioning.Options{{
				Name: "extension", Provider: "custom",
			}},
		}},
	}
	files, err := NewImportManager(nil).GenerateAllInfrastructure(t.Context(), config)
	require.Nil(t, files)
	require.ErrorContains(t, err, `generation is not supported for extension provider "custom"`)
}
