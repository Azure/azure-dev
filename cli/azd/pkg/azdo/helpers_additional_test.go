// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/taskagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestCreateAzureRMServiceEndPointArgs_ServicePrincipalKey(t *testing.T) {
	t.Parallel()

	projectId := uuid.New().String()
	projectName := "demo-project"
	creds := &entraid.AzureCredentials{
		SubscriptionId: "sub-id",
		TenantId:       "tenant-id",
		ClientId:       "client-id",
		ClientSecret:   "shh-secret",
	}

	args, err := createAzureRMServiceEndPointArgs(&projectId, &projectName, creds)
	require.NoError(t, err)

	ep := args.Endpoint
	require.NotNil(t, ep.Authorization.Scheme)
	assert.Equal(t, "ServicePrincipal", *ep.Authorization.Scheme)
	params := *ep.Authorization.Parameters
	assert.Equal(t, "shh-secret", params["serviceprincipalkey"])
	assert.Equal(t, "spnKey", params["authenticationType"])
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

func TestCreateAzureDevPipelineArgs_TerraformMissingRemoteStateErrors(t *testing.T) {
	t.Parallel()

	env := environment.NewWithValues("env-x", map[string]string{
		"AZURE_LOCATION": "eastus",
	})
	queueId := 1
	queueName := "Azure Pipelines"
	queue := &taskagent.TaskAgentQueue{Id: &queueId, Name: &queueName}

	_, err := createAzureDevPipelineArgs(
		"proj-id", "pipeline-name", "repo-name",
		nil, env, queue,
		provisioning.Options{Provider: provisioning.Terraform},
		nil, nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "terraform remote state")
}
