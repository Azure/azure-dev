// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProjectEndpoint = "https://account.services.ai.azure.com/api/projects/project"

func TestResolveProjectEndpointPrefersFlag(t *testing.T) {
	resolved, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		FlagValue: testProjectEndpoint,
		ReadAzdHostedSources: func(context.Context) (azdHostedSources, error) {
			return azdHostedSources{
				EnvValue: "https://other.services.ai.azure.com/api/projects/other",
			}, nil
		},
	})

	require.NoError(t, err)
	assert.Equal(t, testProjectEndpoint, resolved.Endpoint)
}

func TestResolveProjectEndpointUsesPersistedProjectContext(t *testing.T) {
	resolved, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		ReadAzdHostedSources: func(context.Context) (azdHostedSources, error) {
			return azdHostedSources{
				Config: projectContextState{Endpoint: testProjectEndpoint},
			}, nil
		},
	})

	require.NoError(t, err)
	assert.Equal(t, testProjectEndpoint, resolved.Endpoint)
}

func TestResolveProjectEndpointUsesAzureAIHostFallback(t *testing.T) {
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", testProjectEndpoint)

	resolved, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		ReadAzdHostedSources: func(context.Context) (azdHostedSources, error) {
			return azdHostedSources{}, nil
		},
	})

	require.NoError(t, err)
	assert.Equal(t, testProjectEndpoint, resolved.Endpoint)
}

func TestResolveProjectEndpointPrefersFoundryHostVariable(t *testing.T) {
	foundryEndpoint := "https://foundry.services.ai.azure.com/api/projects/project"
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", foundryEndpoint)
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", testProjectEndpoint)

	resolved, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		ReadAzdHostedSources: func(context.Context) (azdHostedSources, error) {
			return azdHostedSources{}, nil
		},
	})

	require.NoError(t, err)
	assert.Equal(t, foundryEndpoint, resolved.Endpoint)
}

func TestValidateProjectEndpointDoesNotDiscloseCredentials(t *testing.T) {
	credential := "user:secret"
	_, err := validateProjectEndpoint(
		"https://" + credential + "@account.services.ai.azure.com/api/projects/project?sig=sensitive",
	)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), credential)
	assert.NotContains(t, err.Error(), "sensitive")
}

func TestValidateProjectEndpointDoesNotDiscloseMalformedURL(t *testing.T) {
	secret := "sensitive"
	_, err := validateProjectEndpoint(
		"https://account.services.ai.azure.com/api/projects/project%zz?sig=" + secret,
	)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
	assert.NotContains(t, err.Error(), "%zz")
}

func TestValidateProjectEndpointRejectsDotSegments(t *testing.T) {
	for _, endpoint := range []string{
		"https://account.services.ai.azure.com/api/projects/.",
		"https://account.services.ai.azure.com/api/projects/..",
		"https://account.services.ai.azure.com/api/projects/%2e%2e",
	} {
		t.Run(endpoint, func(t *testing.T) {
			_, err := validateProjectEndpoint(endpoint)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "dot segments")
		})
	}
}
