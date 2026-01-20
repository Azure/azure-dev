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
