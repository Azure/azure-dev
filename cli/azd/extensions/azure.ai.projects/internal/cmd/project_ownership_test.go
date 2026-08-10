// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	resultPath := filepath.Join(dir, "result.json")
	require.NoError(t, os.WriteFile(requestPath, []byte(`{
		"schemaVersion": 1,
		"source": "azure.ai.projects/direct",
		"unknown": true
	}`), 0600))
	request := &projectInitRequest{}
	require.NoError(t, validateDelegatedPathPair(requestPath, resultPath))
	require.Error(t, decodeDelegatedJSON(requestPath, request))
}

func TestDelegatedResultWritesAtomically(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	resultPath := filepath.Join(dir, "result.json")
	require.NoError(t, os.WriteFile(requestPath, []byte(`{}`), 0600))
	require.NoError(t, writeDelegatedResult(resultPath, map[string]any{"ok": true}))
	var decoded map[string]any
	data, err := os.ReadFile(resultPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, true, decoded["ok"])
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
	assert.True(t, deploymentCommand.Flags().Lookup("result-file").Hidden)
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
