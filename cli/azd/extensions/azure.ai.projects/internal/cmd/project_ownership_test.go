// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestDelegatedProjectInitRequestValidation(t *testing.T) {
	request := &projectInitRequest{
		SchemaVersion: delegatedSchemaVersion,
		Source:        projectInitSourceAgents,
		SourceVersion: "1.0.0-beta.9",
		Project:       delegatedProject{ResourceID: "/subscriptions/s"},
	}
	require.NoError(t, request.validate())

	request.Project.Endpoint = "https://account.services.ai.azure.com/api/projects/p"
	require.Error(t, request.validate())
	request.Project.Endpoint = ""
	request.SchemaVersion = 2
	require.Error(t, request.validate())
}

func TestDelegatedRequestRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	require.NoError(t, os.WriteFile(requestPath, []byte(`{
		"schemaVersion": 1,
		"source": "azure.ai.agents/init",
		"sourceVersion": "1.0.0",
		"unknown": true
	}`), 0600))
	request := &projectInitRequest{}
	require.Error(t, decodeDelegatedJSON(requestPath, request))
}

func TestDelegatedRequestIsInputOnly(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	require.NoError(t, os.WriteFile(requestPath, []byte(`{}`), 0600))
	root := NewRootCommand()
	initCommand, _, err := root.Find([]string{"init"})
	require.NoError(t, err)
	deploymentCommand, _, err := root.Find([]string{"deployment", "add"})
	require.NoError(t, err)
	assert.Nil(t, initCommand.Flags().Lookup("result-file"))
	assert.Nil(t, deploymentCommand.Flags().Lookup("result-file"))
	assert.NoError(t, validateDelegatedFilePath(requestPath, "request", true))
}

func TestProjectCommandsRegistered(t *testing.T) {
	root := NewRootCommand()
	initCommand, _, err := root.Find([]string{"init"})
	require.NoError(t, err)
	assert.Equal(t, "init", initCommand.Name())
	deploymentCommand, _, err := root.Find([]string{"deployment", "add"})
	require.NoError(t, err)
	assert.Equal(t, "add", deploymentCommand.Name())
	assert.Equal(t, "bicep", initCommand.Flags().Lookup("infra").NoOptDefVal)
	assert.True(t, initCommand.Flags().Lookup("request-file").Hidden)
	assert.True(t, deploymentCommand.Flags().Lookup("request-file").Hidden)
}

func TestProjectFileExists(t *testing.T) {
	root := t.TempDir()

	exists, err := projectFileExists(root)
	require.NoError(t, err)
	assert.False(t, exists)

	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yml"), []byte("name: test\n"), 0600))
	exists, err = projectFileExists(root)
	require.NoError(t, err)
	assert.True(t, exists)

	require.NoError(t, os.Remove(filepath.Join(root, "azure.yml")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0600))
	exists, err = projectFileExists(root)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestResolvedProjectFromEndpoint(t *testing.T) {
	project, err := resolvedProjectFromEndpoint(
		"https://account.services.ai.azure.com/api/projects/foundry-project/",
	)
	require.NoError(t, err)
	assert.Equal(t, projectModeExistingEndpoint, project.Mode)
	assert.Equal(t, "account", project.AccountName)
	assert.Equal(t, "foundry-project", project.ProjectName)
	assert.Equal(
		t,
		"https://account.services.ai.azure.com/api/projects/foundry-project",
		project.Endpoint,
	)
}

func TestWriteTerraformEjectedInfra(t *testing.T) {
	for _, test := range []struct {
		name       string
		includeAcr bool
	}{
		{name: "without ACR", includeAcr: false},
		{name: "with ACR", includeAcr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			infraDir := filepath.Join(t.TempDir(), "infra")
			require.NoError(t, os.MkdirAll(infraDir, 0755))
			parameters := map[string]any{
				"includeAcr": test.includeAcr,
				"deployments": []synthesis.Deployment{{
					Name: "chat",
					Model: synthesis.DeploymentModel{
						Format:  "OpenAI",
						Name:    "gpt-4.1",
						Version: "2025-04-14",
					},
					Sku: synthesis.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
				}},
				"connections": []synthesis.Connection{{
					Name:     "search",
					Category: "CognitiveSearch",
					Target:   "https://search.example.com",
					AuthType: "ApiKey",
				}},
				"connectionCredentials": map[string]map[string]any{
					"search": {"key": "${SEARCH_API_KEY}"},
				},
			}

			require.NoError(t, writeTerraformEjectedInfra(infraDir, parameters))

			outputs, err := os.ReadFile(filepath.Join(infraDir, "outputs.tf"))
			require.NoError(t, err)
			assert.Contains(t, string(outputs), "AZURE_AI_PROJECT_ID")
			if test.includeAcr {
				assert.Contains(t, string(outputs), "AZURE_CONTAINER_REGISTRY_ENDPOINT")
				_, err := os.Stat(filepath.Join(infraDir, "acr.tf"))
				assert.NoError(t, err)
			} else {
				assert.NotContains(t, string(outputs), "AZURE_CONTAINER_REGISTRY_ENDPOINT")
				_, err := os.Stat(filepath.Join(infraDir, "acr.tf"))
				assert.ErrorIs(t, err, os.ErrNotExist)
			}

			rawTfvars, err := os.ReadFile(filepath.Join(infraDir, "main.tfvars.json"))
			require.NoError(t, err)
			tfvars := map[string]any{}
			require.NoError(t, json.Unmarshal(rawTfvars, &tfvars))
			assert.Equal(t, "${AZURE_SUBSCRIPTION_ID}", tfvars["subscription_id"])
			assert.Equal(t, "${AZURE_LOCATION}", tfvars["location"])
			assert.Equal(t, "${AZURE_RESOURCE_GROUP}", tfvars["resource_group_name"])
			assert.Equal(t, "${AZURE_ENV_NAME}", tfvars["environment_name"])
			assert.Equal(t, "${AZURE_AI_PROJECT_NAME}", tfvars["foundry_project_name"])
			assert.Equal(t, "${AZURE_PRINCIPAL_ID}", tfvars["principal_id"])
			assert.Equal(t, "${AZD_RESOURCE_TOKEN_SALT}", tfvars["resource_token_salt"])
			assert.NotContains(t, tfvars, "connectionCredentials")
			assert.NotContains(t, tfvars, "includeAcr")

			connections, ok := tfvars["connections"].([]any)
			require.True(t, ok)
			require.Len(t, connections, 1)
			connection, ok := connections[0].(map[string]any)
			require.True(t, ok)
			credentials, ok := connection["credentials"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "${SEARCH_API_KEY}", credentials["key"])
		})
	}
}

func TestProjectServiceNameDeterministic(t *testing.T) {
	services := map[string]*azdext.ServiceConfig{
		"chat-app":     {Host: "azure.ai.agent"},
		"ai-project":   {Host: "custom"},
		"ai-project-2": {Host: "custom"},
	}
	assert.Equal(t, "new-project", projectServiceName("New Project", services))
	assert.Equal(t, "ai-project-3", projectServiceName("", services))
}

func TestLegacyProjectServiceBodyPreservesConfiguration(t *testing.T) {
	body, err := legacyProjectServiceBody(map[string]any{
		"host":        "azure.ai.agents",
		"endpoint":    "https://old.services.ai.azure.com/api/projects/old",
		"deployments": []any{map[string]any{"name": "chat"}},
		"hooks":       map[string]any{"predeploy": "echo ok"},
		"uses":        []any{"connection"},
		"customField": "preserve-me",
	}, "https://new.services.ai.azure.com/api/projects/new")
	require.NoError(t, err)

	assert.NotContains(t, body, "host")
	assert.Equal(t, "https://new.services.ai.azure.com/api/projects/new", body["endpoint"])
	assert.Contains(t, body, "deployments")
	assert.Contains(t, body, "hooks")
	assert.Contains(t, body, "uses")
	assert.Equal(t, "preserve-me", body["customField"])
}

func TestLegacyProjectServiceBodyRemovesEndpointForNewProject(t *testing.T) {
	body, err := legacyProjectServiceBody(map[string]any{
		"host":     "azure.ai.agents",
		"endpoint": "https://old.services.ai.azure.com/api/projects/old",
		"hooks":    map[string]any{"predeploy": "echo ok"},
	}, "")
	require.NoError(t, err)

	assert.NotContains(t, body, "host")
	assert.NotContains(t, body, "endpoint")
	assert.Contains(t, body, "hooks")
}

func TestProjectEnvironmentTransitions(t *testing.T) {
	old := map[string]string{
		"AZURE_AI_PROJECT_ID":            "old-id",
		"AZURE_AI_ACCOUNT_NAME":          "old-account",
		"AZURE_AI_PROJECT_NAME":          "old-project",
		"FOUNDRY_PROJECT_ENDPOINT":       "https://old.services.ai.azure.com/api/projects/old",
		"AZURE_OPENAI_ENDPOINT":          "https://old.openai.azure.com/",
		"AZURE_RESOURCE_GROUP":           "old-rg",
		"AZURE_AI_DEPLOYMENTS_LOCATION":  "eastus",
		"AZURE_AI_MODEL_DEPLOYMENT_NAME": "chat",
	}
	plan := planProjectEnvironment(old, projectModeExistingEndpoint, &resolvedProject{
		Endpoint:    "https://new.services.ai.azure.com/api/projects/new",
		ProjectName: "new",
	}, true)
	assert.Equal(t, "true", plan.Sets["USE_EXISTING_AI_PROJECT"])
	assert.Equal(t, []string{
		"AZURE_AI_ACCOUNT_NAME",
		"AZURE_AI_DEPLOYMENTS_LOCATION",
		"AZURE_AI_MODEL_DEPLOYMENT_NAME",
		"AZURE_AI_PROJECT_ID",
		"AZURE_OPENAI_ENDPOINT",
		"AZURE_RESOURCE_GROUP",
	}, plan.Unsets)
}

func TestProjectEnvironmentClearsOnlyNonEmptyValues(t *testing.T) {
	old := map[string]string{
		"AZURE_AI_PROJECT_ID":   "old-id",
		"AZURE_RESOURCE_GROUP":  "",
		"AZURE_OPENAI_ENDPOINT": "https://old.openai.azure.com/",
	}
	plan := planProjectEnvironment(old, projectModeExistingEndpoint, &resolvedProject{
		Endpoint: "https://new.services.ai.azure.com/api/projects/new",
	}, false)

	assert.Contains(t, plan.Unsets, "AZURE_AI_PROJECT_ID")
	assert.Contains(t, plan.Unsets, "AZURE_OPENAI_ENDPOINT")
	assert.NotContains(t, plan.Unsets, "AZURE_RESOURCE_GROUP")
}

func TestProjectServiceEndpointUsesExactKeyTombstone(t *testing.T) {
	server := grpc.NewServer()
	projectServer := &recordingProjectServiceServer{}
	azdext.RegisterProjectServiceServer(server, projectServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
	})

	require.NoError(t, setProjectServiceEndpoint(
		t.Context(), client, "project", "",
	))
	require.NotNil(t, projectServer.request)
	assert.Equal(t, "project", projectServer.request.ServiceName)
	assert.Equal(t, "endpoint", projectServer.request.Path)
	assert.Equal(t, "", projectServer.request.Value.GetStringValue())
}

type recordingProjectServiceServer struct {
	azdext.UnimplementedProjectServiceServer
	request *azdext.SetServiceConfigValueRequest
}

func (s *recordingProjectServiceServer) SetServiceConfigValue(
	_ context.Context,
	request *azdext.SetServiceConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	s.request = request
	return &azdext.EmptyResponse{}, nil
}

func TestProjectEnvironmentPreservesLocationWhenProjectLocationIsUnknown(t *testing.T) {
	old := map[string]string{
		"AZURE_LOCATION":                "westus2",
		"AZURE_AI_DEPLOYMENTS_LOCATION": "eastus",
		"AZURE_AI_PROJECT_ID":           "old-id",
		"FOUNDRY_PROJECT_ENDPOINT":      "https://old.services.ai.azure.com/api/projects/old",
	}
	plan := planProjectEnvironment(old, projectModeExistingID, &resolvedProject{
		ResourceId: "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/account/projects/new",
		Endpoint: "https://account.services.ai.azure.com/api/projects/new",
	}, true)

	assert.NotContains(t, plan.Sets, "AZURE_LOCATION")
	assert.NotContains(t, plan.Unsets, "AZURE_LOCATION")
}

func TestDeploymentSemanticEqualityIgnoresNameCase(t *testing.T) {
	value := map[string]any{
		"name": "Chat",
		"model": map[string]any{
			"format": "OpenAI", "name": "gpt-4.1", "version": "2025-04-14",
		},
		"sku": map[string]any{"name": "GlobalStandard", "capacity": float64(10)},
	}
	assert.True(t, deploymentSemanticallyEqual(value, synthesisDeploymentForTest()))
}

func synthesisDeploymentForTest() synthesis.Deployment {
	return synthesis.Deployment{
		Name: "chat",
		Model: synthesis.DeploymentModel{
			Format: "OpenAI", Name: "gpt-4.1", Version: "2025-04-14",
		},
		Sku: synthesis.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
	}
}
