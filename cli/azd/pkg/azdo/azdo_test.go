// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/taskagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

func Test_getAzdoConnection(t *testing.T) {
	ctx := t.Context()
	t.Run("empty organization name error", func(t *testing.T) {
		_, err := GetConnection(ctx, "", "")
		assert.EqualError(t, err, "organization name is required")
	})

	t.Run("empty pat error", func(t *testing.T) {
		_, err := GetConnection(ctx, "fake_org", "")
		assert.EqualError(t, err, "personal access token is required")
	})
	t.Run("returns a connection", func(t *testing.T) {
		connection, err := GetConnection(ctx, "fake_org", "fake_pat")
		assert.Nil(t, err)
		assert.NotNil(t, connection)
	})
}

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

func TestGetConnection_BaseUrl(t *testing.T) {
	t.Parallel()

	connection, err := GetConnection(t.Context(), "my-org", "super-secret")
	require.NoError(t, err)
	require.NotNil(t, connection)
	assert.Equal(t, "https://dev.azure.com/my-org", connection.BaseUrl)
	require.NotNil(t, connection.AuthorizationString)
	// PAT connections send the PAT via Basic auth — the string should encode it.
	assert.Contains(t, connection.AuthorizationString, "Basic ")
}

func TestCreateAzureRMServiceEndPointArgs_WorkloadIdentity(t *testing.T) {
	t.Parallel()

	projectId := uuid.New().String()
	projectName := "demo-project"
	creds := &entraid.AzureCredentials{
		SubscriptionId: "sub-id",
		TenantId:       "tenant-id",
		ClientId:       "client-id",
		// No ClientSecret -> WorkloadIdentityFederation path
	}

	args, err := createAzureRMServiceEndPointArgs(&projectId, &projectName, creds)
	require.NoError(t, err)
	require.NotNil(t, args.Endpoint)

	ep := args.Endpoint
	require.NotNil(t, ep.Type)
	assert.Equal(t, "azurerm", *ep.Type)
	require.NotNil(t, ep.Url)
	assert.Equal(t, "https://management.azure.com/", *ep.Url)
	require.NotNil(t, ep.Authorization)
	require.NotNil(t, ep.Authorization.Scheme)
	assert.Equal(t, "WorkloadIdentityFederation", *ep.Authorization.Scheme)

	require.NotNil(t, ep.Authorization.Parameters)
	params := *ep.Authorization.Parameters
	assert.Equal(t, "client-id", params["serviceprincipalid"])
	assert.Equal(t, "tenant-id", params["tenantid"])
	_, hasKey := params["serviceprincipalkey"]
	assert.False(t, hasKey, "should not include key when ClientSecret is empty")

	require.NotNil(t, ep.Data)
	data := *ep.Data
	assert.Equal(t, "sub-id", data["subscriptionId"])
	assert.Equal(t, CloudEnvironment, data["environment"])
	assert.Equal(t, "Subscription", data["scopeLevel"])
	assert.Equal(t, "Manual", data["creationMode"])

	require.NotNil(t, ep.ServiceEndpointProjectReferences)
	refs := *ep.ServiceEndpointProjectReferences
	require.Len(t, refs, 1)
	require.NotNil(t, refs[0].ProjectReference)
	assert.Equal(t, projectName, *refs[0].ProjectReference.Name)
	require.NotNil(t, refs[0].ProjectReference.Id)
	assert.Equal(t, projectId, refs[0].ProjectReference.Id.String())
}

func TestCreateAzureDevPipelineArgs_Bicep(t *testing.T) {
	t.Parallel()

	env := environment.NewWithValues("env-x", map[string]string{
		"AZURE_LOCATION": "eastus",
	})
	creds := &entraid.AzureCredentials{
		SubscriptionId: "sub",
		TenantId:       "tenant",
		ClientId:       "client",
	}
	queueId := 42
	queueName := "Azure Pipelines"
	queue := &taskagent.TaskAgentQueue{
		Id:   &queueId,
		Name: &queueName,
	}

	args, err := createAzureDevPipelineArgs(
		"proj-id", "pipeline-name", "repo-name",
		creds, env, queue,
		provisioning.Options{Provider: provisioning.Bicep},
		nil, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, args.Definition)

	def := args.Definition
	require.NotNil(t, def.Name)
	assert.Equal(t, "pipeline-name", *def.Name)
	require.NotNil(t, def.Repository)
	require.NotNil(t, def.Repository.Type)
	assert.Equal(t, "tfsgit", *def.Repository.Type)
	require.NotNil(t, def.Repository.Name)
	assert.Equal(t, "repo-name", *def.Repository.Name)
	require.NotNil(t, def.Repository.DefaultBranch)
	assert.Equal(t, "refs/heads/"+DefaultBranch, *def.Repository.DefaultBranch)

	require.NotNil(t, def.Queue)
	assert.Equal(t, queueId, *def.Queue.Id)
	assert.Equal(t, queueName, *def.Queue.Name)

	// Process should map to the YAML filename.
	procMap, ok := def.Process.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, AzurePipelineYamlPath, procMap["yamlFilename"])

	// Variables should include the standard set.
	require.NotNil(t, def.Variables)
	m := *def.Variables
	assert.Equal(t, "eastus", *m["AZURE_LOCATION"].Value)
	assert.Equal(t, "env-x", *m["AZURE_ENV_NAME"].Value)
	assert.Equal(t, "sub", *m["AZURE_SUBSCRIPTION_ID"].Value)
}
