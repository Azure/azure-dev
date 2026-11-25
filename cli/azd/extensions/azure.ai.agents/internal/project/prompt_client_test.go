// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
	t.Run("missing projectEndpoint returns dependency error", func(t *testing.T) {
		settings := DefaultPromptAgentSettings()
		err := settings.Validate()
		require.ErrorContains(t, err, "projectEndpoint")
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		require.Equal(t, exterrors.CodeMissingAiProjectEndpoint, localErr.Code)
		require.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
		require.Contains(t, localErr.Suggestion, "azd provision")
	})

	t.Run("present projectEndpoint with missing fields returns validation error", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			settings    PromptAgentSettings
			wantMissing string
			wantAbsent  []string
		}{
			{
				name: "missing subscriptionId and resourceGroup",
				settings: PromptAgentSettings{
					ProjectEndpoint: "https://acct.services.ai.azure.com/api/projects/project",
				},
				wantMissing: "subscriptionId, resourceGroup",
				wantAbsent:  []string{"projectEndpoint"},
			},
			{
				name: "missing subscriptionId only",
				settings: PromptAgentSettings{
					ResourceGroup:   "rg",
					ProjectEndpoint: "https://acct.services.ai.azure.com/api/projects/project",
				},
				wantMissing: "subscriptionId",
				wantAbsent:  []string{"resourceGroup", "projectEndpoint"},
			},
			{
				name: "missing resourceGroup only",
				settings: PromptAgentSettings{
					SubscriptionID:  "sub",
					ProjectEndpoint: "https://acct.services.ai.azure.com/api/projects/project",
				},
				wantMissing: "resourceGroup",
				wantAbsent:  []string{"subscriptionId", "projectEndpoint"},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				err := tc.settings.Validate()
				require.ErrorContains(t, err, tc.wantMissing)
				for _, field := range tc.wantAbsent {
					require.NotContains(t, err.Error(), field)
				}
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, exterrors.CodeInvalidServiceConfig, localErr.Code)
				require.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
				require.Contains(t, localErr.Suggestion, "azd provision")
			})
		}
	})

	t.Run("all required fields valid", func(t *testing.T) {
		settings := PromptAgentSettings{
			SubscriptionID:  "sub",
			ResourceGroup:   "rg",
			ProjectEndpoint: "https://acct.services.ai.azure.com/api/projects/project",
		}
		require.NoError(t, settings.Validate())
	})
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
		PromptAgentResponsesEndpoint(&settings, "", false))
}

func TestResolvePromptTargetFromEnv(t *testing.T) {
	t.Run("standard account", func(t *testing.T) {
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
	})

	t.Run("custom subdomain overrides ARM account name", func(t *testing.T) {
		env := map[string]string{
			"AZURE_SUBSCRIPTION_ID": "sub",
			"AZURE_RESOURCE_GROUP":  "rg",
			"AZURE_AI_PROJECT_ID": "/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.CognitiveServices/accounts/resource-account/projects/project",
			"AZURE_AI_ACCOUNT_NAME":    "resource-account",
			"AZURE_AI_PROJECT_NAME":    "project",
			"FOUNDRY_PROJECT_ENDPOINT": "https://custom-dns.services.ai.azure.com/api/projects/project",
		}

		settings, err := ResolvePromptAgentSettings(env)
		require.NoError(t, err)
		require.Equal(t, env["FOUNDRY_PROJECT_ENDPOINT"], settings.ProjectEndpoint)
		require.Equal(t, "https://custom-dns.services.ai.azure.com", settings.ModelEndpoint)
		require.Equal(t, "https://custom-dns.services.ai.azure.com", settings.EffectiveModelEndpoint())

		applied, err := ResolvePromptTargetFromEnv(settings, env)
		require.NoError(t, err)
		require.True(t, applied)
		require.Equal(t, "https://custom-dns.services.ai.azure.com", settings.EffectiveModelEndpoint())
	})

	t.Run("explicit model endpoint is preserved", func(t *testing.T) {
		settings := PromptAgentSettings{
			ModelEndpoint: "https://explicit.services.ai.azure.com",
		}
		env := map[string]string{
			"AZURE_AI_ACCOUNT_NAME":    "resource-account",
			"FOUNDRY_PROJECT_ENDPOINT": "https://custom-dns.services.ai.azure.com/api/projects/project",
		}

		applied, err := ResolvePromptTargetFromEnv(&settings, env)
		require.NoError(t, err)
		require.True(t, applied)
		require.Equal(t, "https://explicit.services.ai.azure.com", settings.EffectiveModelEndpoint())
	})

	t.Run("falls back to ARM account when endpoint is unset", func(t *testing.T) {
		settings := DefaultPromptAgentSettings()
		env := map[string]string{
			"AZURE_AI_PROJECT_ID": "/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.CognitiveServices/accounts/resource-account/projects/project",
			"AZURE_AI_ACCOUNT_NAME": "resource-account",
		}

		applied, err := ResolvePromptTargetFromEnv(&settings, env)
		require.NoError(t, err)
		require.True(t, applied)
		require.Equal(t, "https://resource-account.services.ai.azure.com", settings.EffectiveModelEndpoint())
	})
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
