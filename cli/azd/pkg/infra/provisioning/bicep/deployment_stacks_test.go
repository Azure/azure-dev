// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"
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

// enableDeploymentStacks turns on the deployment.stacks alpha feature for the duration of the test
// so deploymentOptionsMap resolves the stacks configuration.
func enableDeploymentStacks(t *testing.T) {
	t.Setenv("AZD_ALPHA_ENABLE_DEPLOYMENT_STACKS", "true")
}

func TestDeploymentProjectName(t *testing.T) {
	t.Run("standard deployment uses project identity", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)
		provider.projectName = "project-a"

		require.Equal(t, "project-a", provider.deploymentProjectName())
	})

	t.Run("deployment stacks omit project identity", func(t *testing.T) {
		enableDeploymentStacks(t)
		mockContext := mocks.NewMockContext(t.Context())
		provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)
		provider.projectName = "project-a"

		require.Empty(t, provider.deploymentProjectName())
	})
}

func TestResolveDeploymentStacksMap_Nil(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)
	provider.options.DeploymentStacks = nil

	stacks, err := provider.resolveDeploymentStacksMap(true)
	require.NoError(t, err)
	require.Nil(t, stacks)

	// deploymentOptionsMap must omit the DeploymentStacks key entirely so the API layer
	// applies its own defaults.
	enableDeploymentStacks(t)
	optionsMap, err := provider.deploymentOptionsMap(true)
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

	stacks, err := provider.resolveDeploymentStacksMap(true)
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

	_, err := provider.resolveDeploymentStacksMap(true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "DOES_NOT_EXIST")
}

// TestResolveDeploymentStacksMap_OmitsDenySettingsOnDestroy verifies that the destroy path
// (includeDenySettings=false) never resolves the deny lists, so an unavailable ${VAR} referenced
// only by denySettings can't fail `azd down`. The stack delete APIs consume only actionOnUnmanage.
func TestResolveDeploymentStacksMap_OmitsDenySettingsOnDestroy(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)

	provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
		ActionOnUnmanage: &provisioning.ActionOnUnmanageConfig{
			Resources:      "delete",
			ResourceGroups: "delete",
		},
		DenySettings: &provisioning.DenySettingsConfig{
			Mode: "denyDelete",
			// This variable is intentionally unset; it must not be resolved on the destroy path.
			ExcludedPrincipals: expandableStrings("${DOES_NOT_EXIST}"),
		},
	}

	stacks, err := provider.resolveDeploymentStacksMap(false)
	require.NoError(t, err)
	require.Contains(t, stacks, "actionOnUnmanage")
	require.NotContains(t, stacks, "denySettings")
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

	stacks, err := provider.resolveDeploymentStacksMap(true)
	require.NoError(t, err)

	denySettings, ok := stacks["denySettings"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []string{"33333333-3333-3333-3333-333333333333"}, denySettings["excludedPrincipals"])
}

func TestDeploymentOptionsMap_IncludesResolvedStacks(t *testing.T) {
	enableDeploymentStacks(t)

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

	optionsMap, err := provider.deploymentOptionsMap(true)
	require.NoError(t, err)

	stacks, ok := optionsMap["DeploymentStacks"].(map[string]any)
	require.True(t, ok)
	denySettings, ok := stacks["denySettings"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []string{"44444444-4444-4444-4444-444444444444"}, denySettings["excludedPrincipals"])
}

// TestDeploymentOptionsMap_SkipsResolutionWhenStacksDisabled verifies that when the deployment
// stacks alpha feature is inactive, the DeploymentStacks key is omitted and no ${VAR} resolution
// happens, so an unavailable variable in an inactive deploymentStacks block can't fail an
// otherwise-valid standard provision.
func TestDeploymentOptionsMap_SkipsResolutionWhenStacksDisabled(t *testing.T) {
	// Deployment stacks alpha feature is NOT enabled here.
	mockContext := mocks.NewMockContext(t.Context())
	provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)

	provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
		DenySettings: &provisioning.DenySettingsConfig{
			Mode:               "denyDelete",
			ExcludedPrincipals: expandableStrings("${DOES_NOT_EXIST}"),
		},
	}

	optionsMap, err := provider.deploymentOptionsMap(true)
	require.NoError(t, err)
	require.NotContains(t, optionsMap, "DeploymentStacks")
}

// TestHasActiveDeploymentStacksConfig guards the deployment-state bypass: when an effective
// deployment-stacks configuration is present, Deploy must skip the state shortcut so a changed
// ${VAR}-resolved deny principal/action is re-applied (rather than silently ignored because the
// ARM template and parameters are unchanged) and the ${VAR} validation always runs.
func TestHasActiveDeploymentStacksConfig(t *testing.T) {
	t.Run("no config", func(t *testing.T) {
		enableDeploymentStacks(t)
		mockContext := mocks.NewMockContext(t.Context())
		provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)
		provider.options.DeploymentStacks = nil
		require.False(t, provider.hasActiveDeploymentStacksConfig())
	})

	t.Run("config present but feature disabled", func(t *testing.T) {
		// deployment.stacks alpha feature is NOT enabled.
		mockContext := mocks.NewMockContext(t.Context())
		provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)
		provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
			DenySettings: &provisioning.DenySettingsConfig{Mode: "denyDelete"},
		}
		require.False(t, provider.hasActiveDeploymentStacksConfig())
	})

	t.Run("config present and feature enabled", func(t *testing.T) {
		enableDeploymentStacks(t)
		mockContext := mocks.NewMockContext(t.Context())
		provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)
		provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
			DenySettings: &provisioning.DenySettingsConfig{Mode: "denyDelete"},
		}
		require.True(t, provider.hasActiveDeploymentStacksConfig())
	})
}

// TestResolveDeploymentStacksMap_DefaultExpression verifies that shell-style default expressions
// (${VAR:-fallback}) are honored: an unset variable with a usable default resolves to the default
// and is accepted, rather than being rejected as an unset reference.
func TestResolveDeploymentStacksMap_DefaultExpression(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)

	provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
		DenySettings: &provisioning.DenySettingsConfig{
			Mode:               "denyDelete",
			ExcludedPrincipals: expandableStrings("${UNSET_PRINCIPAL:-55555555-5555-5555-5555-555555555555}"),
		},
	}

	stacks, err := provider.resolveDeploymentStacksMap(true)
	require.NoError(t, err)

	denySettings, ok := stacks["denySettings"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []string{"55555555-5555-5555-5555-555555555555"}, denySettings["excludedPrincipals"])
}

// TestUseDeploymentStateShortcut exercises every branch of the shortcut decision: it is disabled
// when deployment-state tracking is off, when the parameters hash failed, and when an active
// deployment-stacks configuration is present; otherwise it is enabled.
func TestUseDeploymentStateShortcut(t *testing.T) {
	t.Run("enabled by default", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)
		require.True(t, provider.useDeploymentStateShortcut(nil))
	})

	t.Run("disabled when deployment state ignored", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)
		provider.ignoreDeploymentState = true
		require.False(t, provider.useDeploymentStateShortcut(nil))
	})

	t.Run("disabled when parameters hash failed", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)
		require.False(t, provider.useDeploymentStateShortcut(errors.New("hash failed")))
	})

	t.Run("disabled when active deployment stacks config present", func(t *testing.T) {
		enableDeploymentStacks(t)
		mockContext := mocks.NewMockContext(t.Context())
		provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)
		provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
			DenySettings: &provisioning.DenySettingsConfig{Mode: "denyDelete"},
		}
		require.False(t, provider.useDeploymentStateShortcut(nil))
	})

	t.Run("enabled when stacks config present but feature disabled", func(t *testing.T) {
		// deployment.stacks alpha feature is NOT enabled, so the config is inert.
		mockContext := mocks.NewMockContext(t.Context())
		provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)
		provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
			DenySettings: &provisioning.DenySettingsConfig{Mode: "denyDelete"},
		}
		require.True(t, provider.useDeploymentStateShortcut(nil))
	})
}

// TestResolveDeploymentStacksMap_ActionOnUnmanageManagementGroups covers the management-groups
// branch of actionOnUnmanage. resources is set alongside it to mirror a schema-valid config
// (the deployment stacks API requires actionOnUnmanage.resources).
func TestResolveDeploymentStacksMap_ActionOnUnmanageManagementGroups(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)

	provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
		ActionOnUnmanage: &provisioning.ActionOnUnmanageConfig{
			Resources:        "delete",
			ManagementGroups: "detach",
		},
	}

	stacks, err := provider.resolveDeploymentStacksMap(true)
	require.NoError(t, err)

	actionOnUnmanage, ok := stacks["actionOnUnmanage"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "detach", actionOnUnmanage["managementGroups"])
	require.Equal(t, "delete", actionOnUnmanage["resources"])
	require.NotContains(t, stacks, "denySettings")
}

// TestResolveDeploymentStacksValues_BlankLiteralErrors verifies that a literal blank deny-list
// entry (no ${VAR} reference at all) is rejected as a misconfiguration.
func TestResolveDeploymentStacksValues_BlankLiteralErrors(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), nil)

	provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
		DenySettings: &provisioning.DenySettingsConfig{
			Mode:            "denyDelete",
			ExcludedActions: expandableStrings("   "),
		},
	}

	_, err := provider.resolveDeploymentStacksMap(true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty string")
}

// TestResolveDeploymentStacksMap_RoundTripsIntoSDKTypes guards against drift between the string
// keys produced by resolveDeploymentStacksMap and the JSON field names the Deployment Stacks SDK
// types consume. The runtime consumer (azapi.StackDeployments via config.GetSection) JSON-marshals
// this map and unmarshals it into a struct whose fields are armdeploymentstacks.ActionOnUnmanage /
// DenySettings, so this test mirrors that path exactly. If a key here is misspelled or the SDK
// renames a field, the round-trip drops the value and the assertions fail.
func TestResolveDeploymentStacksMap_RoundTripsIntoSDKTypes(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	provider := createBicepProviderWithEnv(t, mockContext, minimalArmTemplate(), map[string]string{
		"OPERATOR_OBJECT_ID": "11111111-1111-1111-1111-111111111111",
	})

	provider.options.DeploymentStacks = &provisioning.DeploymentStacksConfig{
		ActionOnUnmanage: &provisioning.ActionOnUnmanageConfig{
			Resources:        "delete",
			ResourceGroups:   "detach",
			ManagementGroups: "detach",
		},
		DenySettings: &provisioning.DenySettingsConfig{
			Mode:               "denyDelete",
			ApplyToChildScopes: new(true),
			ExcludedActions:    expandableStrings("Microsoft.Authorization/*/write"),
			ExcludedPrincipals: expandableStrings("${OPERATOR_OBJECT_ID}"),
		},
	}

	stacks, err := provider.resolveDeploymentStacksMap(true)
	require.NoError(t, err)

	// Mirror azapi.deploymentStackOptions: the runtime consumer unmarshals the resolved map into
	// the SDK types via config.GetSection (json.Marshal followed by json.Unmarshal).
	var roundTrip struct {
		ActionOnUnmanage *armdeploymentstacks.ActionOnUnmanage `json:"actionOnUnmanage"`
		DenySettings     *armdeploymentstacks.DenySettings     `json:"denySettings"`
	}

	jsonBytes, err := json.Marshal(stacks)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(jsonBytes, &roundTrip))

	require.NotNil(t, roundTrip.ActionOnUnmanage)
	require.NotNil(t, roundTrip.ActionOnUnmanage.Resources)
	require.Equal(t, armdeploymentstacks.DeploymentStacksDeleteDetachEnumDelete, *roundTrip.ActionOnUnmanage.Resources)
	require.NotNil(t, roundTrip.ActionOnUnmanage.ResourceGroups)
	require.Equal(t, armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach, *roundTrip.ActionOnUnmanage.ResourceGroups)
	require.NotNil(t, roundTrip.ActionOnUnmanage.ManagementGroups)
	require.Equal(
		t, armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach, *roundTrip.ActionOnUnmanage.ManagementGroups)

	require.NotNil(t, roundTrip.DenySettings)
	require.NotNil(t, roundTrip.DenySettings.Mode)
	require.Equal(t, armdeploymentstacks.DenySettingsModeDenyDelete, *roundTrip.DenySettings.Mode)
	require.NotNil(t, roundTrip.DenySettings.ApplyToChildScopes)
	require.True(t, *roundTrip.DenySettings.ApplyToChildScopes)
	require.Equal(t, []*string{new("Microsoft.Authorization/*/write")}, roundTrip.DenySettings.ExcludedActions)
	require.Equal(t, []*string{new("11111111-1111-1111-1111-111111111111")}, roundTrip.DenySettings.ExcludedPrincipals)
}
