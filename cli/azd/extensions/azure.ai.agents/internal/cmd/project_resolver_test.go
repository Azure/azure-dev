// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveProjectEndpoint_FlagWins(t *testing.T) {
	// Even with FOUNDRY_PROJECT_ENDPOINT set, the flag should win.
	orig := lookupEnvFunc
	lookupEnvFunc = func(key string) string {
		if key == "FOUNDRY_PROJECT_ENDPOINT" {
			return "https://env.services.ai.azure.com/api/projects/env-proj"
		}
		return ""
	}
	t.Cleanup(func() { lookupEnvFunc = orig })

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		FlagValue: "https://flag.services.ai.azure.com/api/projects/flag-proj",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://flag.services.ai.azure.com/api/projects/flag-proj", result.Endpoint)
	assert.Equal(t, SourceFlag, result.Source)
}

func TestResolveProjectEndpoint_FoundryEnvFallback(t *testing.T) {
	// No flag, no azd client available → falls back to FOUNDRY_PROJECT_ENDPOINT.
	orig := lookupEnvFunc
	lookupEnvFunc = func(key string) string {
		if key == "FOUNDRY_PROJECT_ENDPOINT" {
			return "https://env.services.ai.azure.com/api/projects/env-proj"
		}
		return ""
	}
	t.Cleanup(func() { lookupEnvFunc = orig })

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://env.services.ai.azure.com/api/projects/env-proj", result.Endpoint)
	assert.Equal(t, SourceFoundryEnv, result.Source)
}

func TestResolveProjectEndpoint_NothingResolvable(t *testing.T) {
	orig := lookupEnvFunc
	lookupEnvFunc = func(string) string { return "" }
	t.Cleanup(func() { lookupEnvFunc = orig })

	_, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeMissingProjectEndpoint, localErr.Code)
}

func TestResolveProjectEndpoint_InvalidFlagRejected(t *testing.T) {
	_, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		FlagValue: "http://not-https.services.ai.azure.com/api/projects/p",
	})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolveProjectEndpoint_InvalidFoundryEnvRejected(t *testing.T) {
	orig := lookupEnvFunc
	lookupEnvFunc = func(key string) string {
		if key == "FOUNDRY_PROJECT_ENDPOINT" {
			return "http://bad.services.ai.azure.com/api/projects/p"
		}
		return ""
	}
	t.Cleanup(func() { lookupEnvFunc = orig })

	_, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolveProjectEndpoint_FoundryEnvNormalized(t *testing.T) {
	orig := lookupEnvFunc
	lookupEnvFunc = func(key string) string {
		if key == "FOUNDRY_PROJECT_ENDPOINT" {
			return "  https://X.SERVICES.AI.AZURE.COM/api/projects/p/  "
		}
		return ""
	}
	t.Cleanup(func() { lookupEnvFunc = orig })

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://x.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceFoundryEnv, result.Source)
}
