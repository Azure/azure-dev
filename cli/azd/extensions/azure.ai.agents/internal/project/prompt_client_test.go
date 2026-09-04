// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultPromptAgentSettings(t *testing.T) {
	settings := DefaultPromptAgentSettings()
	require.Empty(t, settings.SubscriptionID)
	require.Empty(t, settings.ResourceGroup)
	require.Empty(t, settings.ProjectEndpoint)
	require.Empty(t, settings.ModelEndpoint)
}

func TestPromptAgentSettingsValidateRequiresFoundryProject(t *testing.T) {
	settings := DefaultPromptAgentSettings()
	require.ErrorContains(t, settings.Validate(), "projectEndpoint")

	settings.SubscriptionID = "sub"
	settings.ResourceGroup = "rg"
	settings.ProjectEndpoint = "https://acct.services.ai.azure.com/api/projects/project"
	require.NoError(t, settings.Validate())
}

func TestPromptAgentSettingsValidateRejectsAuthenticatedCustomEndpoint(t *testing.T) {
	settings := DefaultPromptAgentSettings()
	settings.SubscriptionID = "sub"
	settings.ResourceGroup = "rg"
	settings.ProjectEndpoint = "https://attacker.example/api/projects/project"

	require.ErrorContains(t, settings.Validate(), "HTTPS Foundry project URL")
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

func TestPromptAgentSettingsFromEnvUsesFoundryEndpointOnly(t *testing.T) {
	settings := promptAgentSettingsFromEnv(map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT":  "https://foundry.services.ai.azure.com/api/projects/current",
		"AZURE_AI_PROJECT_ENDPOINT": "https://legacy.services.ai.azure.com/api/projects/old",
	})
	require.Equal(t,
		"https://foundry.services.ai.azure.com/api/projects/current",
		settings.ProjectEndpoint)

	settings = promptAgentSettingsFromEnv(map[string]string{
		"AZURE_AI_PROJECT_ENDPOINT": "https://legacy.services.ai.azure.com/api/projects/old",
	})
	require.Empty(t, settings.ProjectEndpoint)
}
