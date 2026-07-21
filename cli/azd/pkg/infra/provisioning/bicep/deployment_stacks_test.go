// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func expandableStrings(values ...string) []osutil.ExpandableString {
	result := make([]osutil.ExpandableString, 0, len(values))
	for _, value := range values {
		result = append(result, osutil.NewExpandableString(value))
	}
	return result
}

func minimalArmTemplate() azure.ArmTemplate {
	return azure.ArmTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Parameters:     azure.ArmTemplateParameterDefinitions{},
		Outputs:        azure.ArmTemplateOutputs{},
	}
}

func TestResolveDeploymentStacksMap_Nil(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)
	provider.options.DeploymentStacks = nil

	stacks, err := provider.resolveDeploymentStacksMap()
	require.NoError(t, err)
	require.Nil(t, stacks)

	// deploymentOptionsMap must omit the DeploymentStacks key entirely so the API layer
	// applies its own defaults.
	optionsMap, err := provider.deploymentOptionsMap()
	require.NoError(t, err)
	require.NotContains(t, optionsMap, "DeploymentStacks")
}

func TestResolveDeploymentStacksMap_ResolvesEnvSubstitution(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), map[string]string{
		"OPERATOR_OBJECT_ID": "11111111-1111-1111-1111-111111111111",
	})

	// Plan-time layer output takes precedence over the azd environment.
	provider.options.VirtualEnv = map[string]string{
		"PIPELINE_SP_OBJECT_ID": "22222222-2222-2222-2222-222222222222",
	}

	provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
		ActionOnUnmanage: &provisioning.ActionOnUnmanageConfig{
			Resources:      "delete",
			ResourceGroups: "delete",
		},
		DenySettings: &provisioning.DenySettingsConfig{
			Mode:               "denyDelete",
			ApplyToChildScopes: new(true),
			ExcludedActions:    expandableStrings("Microsoft.Authorization/*/write"),
			ExcludedPrincipals: expandableStrings("${PIPELINE_SP_OBJECT_ID}", "${OPERATOR_OBJECT_ID}"),
		},
	}

	stacks, err := provider.resolveDeploymentStacksMap()
	require.NoError(t, err)

	actionOnUnmanage, ok := stacks["actionOnUnmanage"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "delete", actionOnUnmanage["resources"])
	require.Equal(t, "delete", actionOnUnmanage["resourceGroups"])

	denySettings, ok := stacks["denySettings"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "denyDelete", denySettings["mode"])
	require.Equal(t, true, denySettings["applyToChildScopes"])
	require.Equal(t, []string{"Microsoft.Authorization/*/write"}, denySettings["excludedActions"])
	require.Equal(t, []string{
		"22222222-2222-2222-2222-222222222222",
		"11111111-1111-1111-1111-111111111111",
	}, denySettings["excludedPrincipals"])
}

func TestResolveDeploymentStacksMap_UnsetVarErrors(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)

	provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
		DenySettings: &provisioning.DenySettingsConfig{
			Mode:               "denyDelete",
			ExcludedPrincipals: expandableStrings("${DOES_NOT_EXIST}"),
		},
	}

	_, err := provider.resolveDeploymentStacksMap()
	require.Error(t, err)
	require.Contains(t, err.Error(), "DOES_NOT_EXIST")
}

func TestResolveDeploymentStacksMap_EnvFallback(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), map[string]string{
		"OPERATOR_OBJECT_ID": "33333333-3333-3333-3333-333333333333",
	})

	provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
		DenySettings: &provisioning.DenySettingsConfig{
			Mode:               "denyWriteAndDelete",
			ExcludedPrincipals: expandableStrings("${OPERATOR_OBJECT_ID}"),
		},
	}

	stacks, err := provider.resolveDeploymentStacksMap()
	require.NoError(t, err)

	denySettings := stacks["denySettings"].(map[string]any)
	require.Equal(t, []string{"33333333-3333-3333-3333-333333333333"}, denySettings["excludedPrincipals"])
}

func TestDeploymentOptionsMap_IncludesResolvedStacks(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), map[string]string{
		"OPERATOR_OBJECT_ID": "44444444-4444-4444-4444-444444444444",
	})

	provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
		DenySettings: &provisioning.DenySettingsConfig{
			Mode:               "denyDelete",
			ExcludedPrincipals: expandableStrings("${OPERATOR_OBJECT_ID}"),
		},
	}

	optionsMap, err := provider.deploymentOptionsMap()
	require.NoError(t, err)

	stacks, ok := optionsMap["DeploymentStacks"].(map[string]any)
	require.True(t, ok)
	denySettings := stacks["denySettings"].(map[string]any)
	require.Equal(t, []string{"44444444-4444-4444-4444-444444444444"}, denySettings["excludedPrincipals"])
}
