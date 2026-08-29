// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultPromptAgentSettings(t *testing.T) {
	settings := DefaultPromptAgentSettings()
	require.Equal(t, DefaultPromptAPIVersion, settings.APIVersion)
	require.Equal(t, DefaultPromptModelEndpoint, settings.ModelEndpoint)
}

func TestPromptAgentSettingsValidateRequiresFoundryProject(t *testing.T) {
	settings := DefaultPromptAgentSettings()
	require.ErrorContains(t, settings.Validate(), "projectEndpoint")

	settings.ProjectEndpoint = "https://acct.services.ai.azure.com/api/projects/project"
	require.NoError(t, settings.Validate())
}

func TestNewPromptAgentClientUsesFoundryProject(t *testing.T) {
	t.Setenv(PromptNoAuthEnvVar, "true")
	settings := DefaultPromptAgentSettings()
	settings.ProjectEndpoint = "https://acct.services.ai.azure.com/api/projects/project"
	client, err := NewPromptAgentClient(&settings, nil)
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestPromptAgentResponsesEndpoint(t *testing.T) {
	settings := PromptAgentSettings{ProjectEndpoint: "https://acct.services.ai.azure.com/api/projects/project"}
	require.Equal(t,
		"https://acct.services.ai.azure.com/api/projects/project/openai/v1/responses",
		promptAgentResponsesEndpoint(&settings))
}

func TestResolvePromptTargetFromEnv(t *testing.T) {
	settings := DefaultPromptAgentSettings()
	env := map[string]string{
		"AZURE_SUBSCRIPTION_ID":    "sub",
		"AZURE_RESOURCE_GROUP":     "rg",
		"FOUNDRY_PROJECT_ENDPOINT": "https://acct.services.ai.azure.com/api/projects/project",
		"AZURE_AI_PROJECT_NAME":    "project",
		"AZURE_AI_ACCOUNT_NAME":    "acct",
	}

	applied, err := ResolvePromptTargetFromEnv(&settings, env)
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, env["FOUNDRY_PROJECT_ENDPOINT"], settings.ProjectEndpoint)
	require.Equal(t, "https://acct.services.ai.azure.com", settings.EffectiveModelEndpoint())
}

func TestResolvePromptAgentSettingsExpandsConfiguredValues(t *testing.T) {
	settings, err := ResolvePromptAgentSettings(&PromptAgentSettings{
		ProjectEndpoint: "${PROJECT_ENDPOINT}",
		ModelEndpoint:   "${MODEL_ENDPOINT}",
	}, map[string]string{
		"PROJECT_ENDPOINT": "https://acct.services.ai.azure.com/api/projects/project",
		"MODEL_ENDPOINT":   "https://acct.services.ai.azure.com",
	})
	require.NoError(t, err)
	require.Equal(t, "https://acct.services.ai.azure.com/api/projects/project", settings.ProjectEndpoint)
	require.Equal(t, "https://acct.services.ai.azure.com", settings.ModelEndpoint)
}
