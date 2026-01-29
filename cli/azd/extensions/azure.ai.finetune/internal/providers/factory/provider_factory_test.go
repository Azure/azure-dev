// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package factory

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAzureOpenAIConstants validates constants required for Azure OpenAI API integration.
// Invalid values here would cause hard-to-debug runtime failures when connecting to Azure.
func TestAzureOpenAIConstants(t *testing.T) {
	t.Run("APIVersionFormat", func(t *testing.T) {
		// Azure API versions follow YYYY-MM-DD format (possibly with -preview suffix)
		// Using wrong format causes 400 Bad Request errors from Azure
		require.NotEmpty(t, DefaultApiVersion)
		require.Regexp(t, `^\d{4}-\d{2}-\d{2}(-preview)?$`, DefaultApiVersion,
			"API version should follow YYYY-MM-DD or YYYY-MM-DD-preview format")
	})

	t.Run("EndpointURLFormat", func(t *testing.T) {
		// Endpoint must be HTTPS for security and contain %s for resource name substitution
		require.NotEmpty(t, DefaultCognitiveServicesEndpoint)
		require.True(t, strings.HasPrefix(DefaultCognitiveServicesEndpoint, "https://"),
			"Endpoint must use HTTPS for secure API calls")
		require.Contains(t, DefaultCognitiveServicesEndpoint, "%s",
			"Endpoint must contain placeholder for resource name substitution")
	})

	t.Run("OAuthScopeFormat", func(t *testing.T) {
		// Azure OAuth2 scopes must end with .default for proper token acquisition
		require.NotEmpty(t, DefaultAzureFinetuningScope)
		require.True(t, strings.HasSuffix(DefaultAzureFinetuningScope, ".default"),
			"Azure scope must end with .default for OAuth2 token flow")
	})
}

func TestNewModelDeploymentProvider_WithNilCredential(t *testing.T) {
	// Test that NewModelDeploymentProvider can be created with nil credential
	// Note: The Azure SDK allows nil credential during factory creation
	// (validation happens later when making actual API calls)
	provider, err := NewModelDeploymentProvider("test-subscription-id", nil)

	// Azure SDK defers credential validation until API call time
	require.NoError(t, err)
	require.NotNil(t, provider)
}

func TestNewModelDeploymentProvider_EmptySubscriptionID(t *testing.T) {
	// Empty subscription ID is accepted at construction time
	// (validation happens when making actual API calls)
	provider, err := NewModelDeploymentProvider("", nil)

	require.NoError(t, err)
	require.NotNil(t, provider)
}

func TestNewModelDeploymentProvider_ValidSubscriptionID(t *testing.T) {
	// Test with a valid-looking subscription ID
	subscriptionID := "12345678-1234-1234-1234-123456789012"
	provider, err := NewModelDeploymentProvider(subscriptionID, nil)

	require.NoError(t, err)
	require.NotNil(t, provider)
}

func TestDefaultConstants_AreConsistent(t *testing.T) {
	// Verify that default constants are consistent with each other
	t.Run("EndpointHasTwoPlaceholders", func(t *testing.T) {
		// Endpoint should have exactly 2 %s placeholders (account name and project name)
		placeholderCount := strings.Count(DefaultCognitiveServicesEndpoint, "%s")
		require.Equal(t, 2, placeholderCount,
			"Endpoint should have 2 placeholders for account name and project name")
	})

	t.Run("APIVersionIsRecent", func(t *testing.T) {
		// API version should be from 2024 or later (sanity check)
		require.True(t, strings.HasPrefix(DefaultApiVersion, "202"),
			"API version should be recent (2020s)")
	})

	t.Run("ScopeIsAzureAI", func(t *testing.T) {
		// Scope should be for Azure AI services
		require.Contains(t, DefaultAzureFinetuningScope, "ai.azure.com",
			"Scope should be for Azure AI services")
	})
}
