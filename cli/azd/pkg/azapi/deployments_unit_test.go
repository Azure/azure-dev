// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAzCliDeploymentOutput_Secured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		typeName string
		want     bool
	}{
		{"SecureString", "SecureString", true},
		{"securestring_lower", "securestring", true},
		{"SECURESTRING_upper", "SECURESTRING", true},
		{"SecureObject", "SecureObject", true},
		{"secureobject_lower", "secureobject", true},
		{"SECUREOBJECT_upper", "SECUREOBJECT", true},
		{"plain_string", "String", false},
		{"plain_int", "Int", false},
		{"plain_bool", "Bool", false},
		{"plain_object", "Object", false},
		{"plain_array", "Array", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			o := AzCliDeploymentOutput{Type: tt.typeName}
			assert.Equal(t, tt.want, o.Secured())
		})
	}
}

func TestCreateDeploymentOutput(t *testing.T) {
	t.Parallel()

	t.Run("nil_returns_empty_map", func(t *testing.T) {
		t.Parallel()
		result := CreateDeploymentOutput(nil)
		require.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("single_output", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{
			"endpoint": map[string]any{
				"type":  "String",
				"value": "https://example.com",
			},
		}
		result := CreateDeploymentOutput(raw)
		require.Len(t, result, 1)
		assert.Equal(t, "String", result["endpoint"].Type)
		assert.Equal(t, "https://example.com", result["endpoint"].Value)
	})

	t.Run("multiple_outputs", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{
			"endpoint": map[string]any{
				"type":  "String",
				"value": "https://example.com",
			},
			"key": map[string]any{
				"type":  "SecureString",
				"value": "secret-key-123",
			},
			"count": map[string]any{
				"type":  "Int",
				"value": float64(42),
			},
		}
		result := CreateDeploymentOutput(raw)
		require.Len(t, result, 3)
		assert.True(t, result["key"].Secured())
		assert.False(t, result["endpoint"].Secured())
		assert.Equal(t, float64(42), result["count"].Value)
	})
}

func TestConvertFromStandardProvisioningState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input armresources.ProvisioningState
		want  DeploymentProvisioningState
	}{
		{"Accepted", armresources.ProvisioningStateAccepted,
			DeploymentProvisioningStateAccepted},
		{"Canceled", armresources.ProvisioningStateCanceled,
			DeploymentProvisioningStateCanceled},
		{"Creating", armresources.ProvisioningStateCreating,
			DeploymentProvisioningStateCreating},
		{"Deleted", armresources.ProvisioningStateDeleted,
			DeploymentProvisioningStateDeleted},
		{"Deleting", armresources.ProvisioningStateDeleting,
			DeploymentProvisioningStateDeleting},
		{"Failed", armresources.ProvisioningStateFailed,
			DeploymentProvisioningStateFailed},
		{"NotSpecified", armresources.ProvisioningStateNotSpecified,
			DeploymentProvisioningStateNotSpecified},
		{"Ready", armresources.ProvisioningStateReady,
			DeploymentProvisioningStateReady},
		{"Running", armresources.ProvisioningStateRunning,
			DeploymentProvisioningStateRunning},
		{"Succeeded", armresources.ProvisioningStateSucceeded,
			DeploymentProvisioningStateSucceeded},
		{"Updating", armresources.ProvisioningStateUpdating,
			DeploymentProvisioningStateUpdating},
		{"unknown_returns_empty",
			armresources.ProvisioningState("SomethingNew"),
			DeploymentProvisioningState("")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := convertFromStandardProvisioningState(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConvertFromStacksProvisioningState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input armdeploymentstacks.DeploymentStackProvisioningState
		want  DeploymentProvisioningState
	}{
		{"Canceled",
			armdeploymentstacks.DeploymentStackProvisioningStateCanceled,
			DeploymentProvisioningStateCanceled},
		{"Canceling",
			armdeploymentstacks.DeploymentStackProvisioningStateCanceling,
			DeploymentProvisioningStateCanceling},
		{"Creating",
			armdeploymentstacks.DeploymentStackProvisioningStateCreating,
			DeploymentProvisioningStateCreating},
		{"Deleting",
			armdeploymentstacks.DeploymentStackProvisioningStateDeleting,
			DeploymentProvisioningStateDeleting},
		{"DeletingResources",
			armdeploymentstacks.DeploymentStackProvisioningStateDeletingResources,
			DeploymentProvisioningStateDeletingResources},
		{"Deploying",
			armdeploymentstacks.DeploymentStackProvisioningStateDeploying,
			DeploymentProvisioningStateDeploying},
		{"Failed",
			armdeploymentstacks.DeploymentStackProvisioningStateFailed,
			DeploymentProvisioningStateFailed},
		{"Succeeded",
			armdeploymentstacks.DeploymentStackProvisioningStateSucceeded,
			DeploymentProvisioningStateSucceeded},
		{"UpdatingDenyAssignments",
			armdeploymentstacks.DeploymentStackProvisioningStateUpdatingDenyAssignments,
			DeploymentProvisioningStateUpdatingDenyAssignments},
		{"Validating",
			armdeploymentstacks.DeploymentStackProvisioningStateValidating,
			DeploymentProvisioningStateValidating},
		{"Waiting",
			armdeploymentstacks.DeploymentStackProvisioningStateWaiting,
			DeploymentProvisioningStateWaiting},
		{"unknown_returns_empty",
			armdeploymentstacks.DeploymentStackProvisioningState("Unknown"),
			DeploymentProvisioningState("")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := convertFromStacksProvisioningState(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStackDeployments_GenerateDeploymentName(t *testing.T) {
	t.Parallel()

	sd := &StackDeployments{}

	tests := []struct {
		name     string
		baseName string
		want     string
	}{
		{"simple", "my-env", "azd-stack-my-env"},
		{"empty", "", "azd-stack-"},
		{"complex", "a-b-c-123", "azd-stack-a-b-c-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sd.GenerateDeploymentName(tt.baseName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGroupByResourceGroup(t *testing.T) {
	t.Parallel()

	t.Run("nil_input", func(t *testing.T) {
		t.Parallel()
		result, err := GroupByResourceGroup(nil)
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("empty_input", func(t *testing.T) {
		t.Parallel()
		result, err := GroupByResourceGroup(
			[]*armresources.ResourceReference{},
		)
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("groups_resources_correctly", func(t *testing.T) {
		t.Parallel()
		refs := []*armresources.ResourceReference{
			{ID: new(
				"/subscriptions/sub1/resourceGroups/rg1" +
					"/providers/Microsoft.Web/sites/app1")},
			{ID: new(
				"/subscriptions/sub1/resourceGroups/rg1" +
					"/providers/Microsoft.Storage/storageAccounts/sa1")},
			{ID: new(
				"/subscriptions/sub1/resourceGroups/rg2" +
					"/providers/Microsoft.Web/sites/app2")},
		}

		result, err := GroupByResourceGroup(refs)
		require.NoError(t, err)
		require.Len(t, result, 2)
		assert.Len(t, result["rg1"], 2)
		assert.Len(t, result["rg2"], 1)
	})

	t.Run("excludes_resource_group_type", func(t *testing.T) {
		t.Parallel()
		refs := []*armresources.ResourceReference{
			{ID: new(
				"/subscriptions/sub1/resourceGroups/rg1")},
			{ID: new(
				"/subscriptions/sub1/resourceGroups/rg1" +
					"/providers/Microsoft.Web/sites/app1")},
		}

		result, err := GroupByResourceGroup(refs)
		require.NoError(t, err)
		require.Len(t, result, 1)
		// Only the web app, not the resource group itself
		assert.Len(t, result["rg1"], 1)
		assert.Equal(t, "app1", result["rg1"][0].Name)
	})

	t.Run("invalid_resource_id", func(t *testing.T) {
		t.Parallel()
		refs := []*armresources.ResourceReference{
			{ID: new("not-a-valid-resource-id")},
		}

		_, err := GroupByResourceGroup(refs)
		require.Error(t, err)
	})

	t.Run("subscription_level_resources_skipped", func(t *testing.T) {
		t.Parallel()
		refs := []*armresources.ResourceReference{
			{ID: new(
				"/subscriptions/sub1/providers" +
					"/Microsoft.Resources/deployments/deploy1")},
		}

		result, err := GroupByResourceGroup(refs)
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestIsNotLoggedInMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			"no_subscription_found",
			"ERROR: No subscription found",
			true,
		},
		{
			"please_run_az_login_single_quotes",
			"Please run 'az login' to setup account.",
			true,
		},
		{
			"please_run_az_login_double_quotes",
			`Please run "az login" to access your accounts.`,
			true,
		},
		{
			"unrelated_message",
			"deployment succeeded",
			false,
		},
		{
			"empty_string",
			"",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isNotLoggedInMessage(tt.input))
		})
	}
}

func TestIsRefreshTokenExpiredMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			"AADSTS70043",
			"AADSTS70043: The refresh token has expired",
			true,
		},
		{
			"AADSTS700082",
			"AADSTS700082: expired due to inactivity",
			true,
		},
		{
			"unrelated_error",
			"AADSTS50001: something else",
			false,
		},
		{
			"empty",
			"",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want,
				isRefreshTokenExpiredMessage(tt.input))
		})
	}
}
