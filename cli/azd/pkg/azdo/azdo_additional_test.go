// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateBuildDefinitionVariable(t *testing.T) {
	t.Run("standard variable", func(t *testing.T) {
		v := createBuildDefinitionVariable("value1", false, false)
		assert.Equal(t, "value1", *v.Value)
		assert.False(t, *v.IsSecret)
		assert.False(t, *v.AllowOverride)
	})

	t.Run("secret variable", func(t *testing.T) {
		v := createBuildDefinitionVariable("secret", true, false)
		assert.Equal(t, "secret", *v.Value)
		assert.True(t, *v.IsSecret)
		assert.False(t, *v.AllowOverride)
	})

	t.Run("overridable variable", func(t *testing.T) {
		v := createBuildDefinitionVariable("val", false, true)
		assert.True(t, *v.AllowOverride)
	})
}

func TestGetDefinitionVariables_Bicep(t *testing.T) {
	env := environment.NewWithValues(
		"test-env",
		map[string]string{
			"AZURE_LOCATION": "eastus2",
		},
	)

	creds := &entraid.AzureCredentials{
		SubscriptionId: "sub-123",
		TenantId:       "tenant-456",
		ClientId:       "client-789",
		ClientSecret:   "secret-abc",
	}

	opts := provisioning.Options{
		Provider: provisioning.Bicep,
	}

	vars, err := getDefinitionVariables(
		env, creds, opts, nil, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, vars)

	m := *vars

	// Standard variables
	assert.Equal(t, "eastus2", *m["AZURE_LOCATION"].Value)
	assert.Equal(t, "test-env", *m["AZURE_ENV_NAME"].Value)
	assert.Equal(
		t, ServiceConnectionName,
		*m["AZURE_SERVICE_CONNECTION"].Value,
	)
	assert.Equal(
		t, "sub-123", *m["AZURE_SUBSCRIPTION_ID"].Value,
	)

	// Should NOT have Terraform-specific variables
	_, hasTenantID := m["ARM_TENANT_ID"]
	assert.False(t, hasTenantID)
}

func TestGetDefinitionVariables_BicepWithResourceGroup(t *testing.T) {
	env := environment.NewWithValues(
		"test-env",
		map[string]string{
			"AZURE_LOCATION":       "westus",
			"AZURE_RESOURCE_GROUP": "my-rg",
		},
	)

	opts := provisioning.Options{
		Provider: provisioning.Bicep,
	}

	vars, err := getDefinitionVariables(
		env, nil, opts, nil, nil,
	)
	require.NoError(t, err)

	m := *vars

	// Bicep with resource group should include it
	assert.Equal(t, "my-rg", *m["AZURE_RESOURCE_GROUP"].Value)
}

func TestGetDefinitionVariables_Terraform(t *testing.T) {
	env := environment.NewWithValues(
		"test-env",
		map[string]string{
			"AZURE_LOCATION":     "eastus",
			"RS_RESOURCE_GROUP":  "tf-state-rg",
			"RS_STORAGE_ACCOUNT": "tfstatestorage",
			"RS_CONTAINER_NAME":  "tfstate",
		},
	)

	creds := &entraid.AzureCredentials{
		SubscriptionId: "sub-123",
		TenantId:       "tenant-456",
		ClientId:       "client-789",
		ClientSecret:   "secret-abc",
	}

	opts := provisioning.Options{
		Provider: provisioning.Terraform,
	}

	vars, err := getDefinitionVariables(
		env, creds, opts, nil, nil,
	)
	require.NoError(t, err)

	m := *vars

	// Terraform-specific ARM variables
	assert.Equal(t, "tenant-456", *m["ARM_TENANT_ID"].Value)
	assert.Equal(t, "client-789", *m["ARM_CLIENT_ID"].Value)
	assert.True(t, *m["ARM_CLIENT_ID"].IsSecret)
	assert.Equal(
		t, "secret-abc", *m["ARM_CLIENT_SECRET"].Value,
	)
	assert.True(t, *m["ARM_CLIENT_SECRET"].IsSecret)

	// Terraform remote state variables
	assert.Equal(
		t, "tf-state-rg", *m["RS_RESOURCE_GROUP"].Value,
	)
	assert.Equal(
		t, "tfstatestorage", *m["RS_STORAGE_ACCOUNT"].Value,
	)
	assert.Equal(
		t, "tfstate", *m["RS_CONTAINER_NAME"].Value,
	)
}

func TestGetDefinitionVariables_TerraformMissingRemoteState(
	t *testing.T,
) {
	env := environment.NewWithValues(
		"test-env",
		map[string]string{
			"AZURE_LOCATION": "eastus",
		},
	)

	opts := provisioning.Options{
		Provider: provisioning.Terraform,
	}

	_, err := getDefinitionVariables(env, nil, opts, nil, nil)
	require.Error(t, err)
	assert.Contains(
		t, err.Error(),
		"terraform remote state is not correctly configured",
	)
}

func TestGetDefinitionVariables_AdditionalSecretsAndVars(
	t *testing.T,
) {
	env := environment.NewWithValues(
		"test-env",
		map[string]string{
			"AZURE_LOCATION": "eastus",
		},
	)

	opts := provisioning.Options{
		Provider: provisioning.Bicep,
	}

	secrets := map[string]string{
		"MY_SECRET": "secret-value",
	}
	variables := map[string]string{
		"MY_VAR": "var-value",
	}

	vars, err := getDefinitionVariables(
		env, nil, opts, secrets, variables,
	)
	require.NoError(t, err)

	m := *vars

	// Additional secrets should be marked as secret
	assert.Equal(t, "secret-value", *m["MY_SECRET"].Value)
	assert.True(t, *m["MY_SECRET"].IsSecret)

	// Additional variables should allow override
	assert.Equal(t, "var-value", *m["MY_VAR"].Value)
	assert.True(t, *m["MY_VAR"].AllowOverride)
}

func TestGetDefinitionVariables_NilCredentials(t *testing.T) {
	env := environment.NewWithValues(
		"test-env",
		map[string]string{
			"AZURE_LOCATION": "eastus",
		},
	)

	opts := provisioning.Options{
		Provider: provisioning.Bicep,
	}

	vars, err := getDefinitionVariables(
		env, nil, opts, nil, nil,
	)
	require.NoError(t, err)

	m := *vars

	// Should not have subscription ID without credentials
	_, hasSubId := m["AZURE_SUBSCRIPTION_ID"]
	assert.False(t, hasSubId)
}

func TestConstants(t *testing.T) {
	assert.Equal(t, "dev.azure.com", AzDoHostName)
	assert.Equal(t, "AZURE_DEVOPS_EXT_PAT", AzDoPatName)
	assert.Equal(
		t, "AZURE_DEVOPS_ORG_NAME", AzDoEnvironmentOrgName,
	)
	assert.Equal(
		t, ".azdo/pipelines/azure-dev.yml", AzurePipelineYamlPath,
	)
	assert.Equal(t, "main", DefaultBranch)
	assert.Equal(t, "azconnection", ServiceConnectionName)
}
