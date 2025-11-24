// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInfraConfig_ToProvisioningOptions(t *testing.T) {
	infraConfig := InfraConfig{
		Provider: "terraform",
		Path:     "custom-infra",
		Module:   "custom-module",
		Name:     "test-infra",
		Layers: []InfraConfig{
			{
				Name:     "layer1",
				Provider: "bicep",
				Path:     "layer1-path",
				Module:   "layer1-module",
			},
		},
		DeploymentStacks: map[string]any{
			"key": "value",
		},
	}

	provOpts := infraConfig.ToProvisioningOptions()

	assert.Equal(t, provisioning.ProviderKind("terraform"), provOpts.Provider)
	assert.Equal(t, "custom-infra", provOpts.Path)
	assert.Equal(t, "custom-module", provOpts.Module)
	assert.Equal(t, "test-infra", provOpts.Name)
	assert.Equal(t, map[string]any{"key": "value"}, provOpts.DeploymentStacks)

	require.Len(t, provOpts.Layers, 1)
	assert.Equal(t, "layer1", provOpts.Layers[0].Name)
	assert.Equal(t, provisioning.ProviderKind("bicep"), provOpts.Layers[0].Provider)
	assert.Equal(t, "layer1-path", provOpts.Layers[0].Path)
	assert.Equal(t, "layer1-module", provOpts.Layers[0].Module)
}

func TestInfraConfigFromProvisioningOptions(t *testing.T) {
	provOpts := provisioning.Options{
		Provider: provisioning.Terraform,
		Path:     "custom-infra",
		Module:   "custom-module",
		Name:     "test-infra",
		Layers: []provisioning.Options{
			{
				Name:     "layer1",
				Provider: provisioning.Bicep,
				Path:     "layer1-path",
				Module:   "layer1-module",
			},
		},
		DeploymentStacks: map[string]any{
			"key": "value",
		},
	}

	infraConfig := InfraConfigFromProvisioningOptions(provOpts)

	assert.Equal(t, "terraform", infraConfig.Provider)
	assert.Equal(t, "custom-infra", infraConfig.Path)
	assert.Equal(t, "custom-module", infraConfig.Module)
	assert.Equal(t, "test-infra", infraConfig.Name)
	assert.Equal(t, map[string]any{"key": "value"}, infraConfig.DeploymentStacks)

	require.Len(t, infraConfig.Layers, 1)
	assert.Equal(t, "layer1", infraConfig.Layers[0].Name)
	assert.Equal(t, "bicep", infraConfig.Layers[0].Provider)
	assert.Equal(t, "layer1-path", infraConfig.Layers[0].Path)
	assert.Equal(t, "layer1-module", infraConfig.Layers[0].Module)
}

func TestInfraConfig_RoundTrip(t *testing.T) {
	original := provisioning.Options{
		Provider: provisioning.Terraform,
		Path:     "infra",
		Module:   "main",
		Name:     "my-infra",
		Layers: []provisioning.Options{
			{
				Name:     "networking",
				Provider: provisioning.Bicep,
				Path:     "infra/network",
				Module:   "network",
			},
			{
				Name:     "application",
				Provider: provisioning.Terraform,
				Path:     "infra/app",
				Module:   "app",
			},
		},
		DeploymentStacks: map[string]any{
			"enabled": true,
		},
	}

	// Convert to InfraConfig and back
	infraConfig := InfraConfigFromProvisioningOptions(original)
	result := infraConfig.ToProvisioningOptions()

	// Verify round-trip conversion
	assert.Equal(t, original.Provider, result.Provider)
	assert.Equal(t, original.Path, result.Path)
	assert.Equal(t, original.Module, result.Module)
	assert.Equal(t, original.Name, result.Name)
	assert.Equal(t, original.DeploymentStacks, result.DeploymentStacks)

	require.Len(t, result.Layers, 2)
	for i := range original.Layers {
		assert.Equal(t, original.Layers[i].Name, result.Layers[i].Name)
		assert.Equal(t, original.Layers[i].Provider, result.Layers[i].Provider)
		assert.Equal(t, original.Layers[i].Path, result.Layers[i].Path)
		assert.Equal(t, original.Layers[i].Module, result.Layers[i].Module)
	}
}
