// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"azure.ai.projects/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubReadAzdHostedSources returns a function suitable for the
// resolveProjectEndpointOpts.ReadAzdHostedSources seam. Each call returns a
// fresh closure so no test mutates state shared with other tests.
func stubReadAzdHostedSources(
	sources azdHostedSources,
	err error,
) func(context.Context) (azdHostedSources, error) {
	return func(context.Context) (azdHostedSources, error) {
		return sources, err
	}
}

// isolateFromAzdDaemon returns resolver opts whose ReadAzdHostedSources
// reports no hosted sources, and clears AZD_SERVER so any code path that
// bypasses the stub cannot reach a real daemon. The resolver under test then
// only sees the flag and FOUNDRY_PROJECT_ENDPOINT.
func isolateFromAzdDaemon(t *testing.T) resolveProjectEndpointOpts {
	t.Helper()
	t.Setenv("AZD_SERVER", "")
	return resolveProjectEndpointOpts{
		ReadAzdHostedSources: stubReadAzdHostedSources(azdHostedSources{}, nil),
	}
}

func TestResolveProjectEndpoint_FlagWins(t *testing.T) {
	// Even with FOUNDRY_PROJECT_ENDPOINT and azd-hosted sources set, the flag should win.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://env.services.ai.azure.com/api/projects/env-proj")

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		FlagValue: "https://flag.services.ai.azure.com/api/projects/flag-proj",
		ReadAzdHostedSources: stubReadAzdHostedSources(azdHostedSources{
			EnvValue: "https://azdenv.services.ai.azure.com/api/projects/p",
			EnvName:  "dev",
		}, nil),
	})
	require.NoError(t, err)
	assert.Equal(t, "https://flag.services.ai.azure.com/api/projects/flag-proj", result.Endpoint)
	assert.Equal(t, SourceFlag, result.Source)
}

func TestResolveProjectEndpoint_AzdEnvResolves(t *testing.T) {
	// Level 2: AZURE_AI_PROJECT_ENDPOINT from the active azd env wins over
	// global config and FOUNDRY_PROJECT_ENDPOINT.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		ReadAzdHostedSources: stubReadAzdHostedSources(azdHostedSources{
			EnvValue: "  HTTPS://Azdenv.Services.AI.Azure.com/api/projects/p/  ",
			EnvName:  "dev",
			CfgState: projectContextState{
				Endpoint: "https://cfg.services.ai.azure.com/api/projects/p",
				SetAt:    "2025-01-01T00:00:00Z",
			},
			CfgFound: true,
		}, nil),
	})
	require.NoError(t, err)
	assert.Equal(t, "https://azdenv.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceAzdEnv, result.Source)
	assert.Equal(t, "dev", result.AzdEnvName)
}

func TestResolveProjectEndpoint_AzdEnvInvalidRejected(t *testing.T) {
	// Level 2 invalid values are hard errors (no silent fallback to lower levels).
	// FOUNDRY_PROJECT_ENDPOINT is set, but resolver must NOT fall through to it.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")

	_, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		ReadAzdHostedSources: stubReadAzdHostedSources(azdHostedSources{
			EnvValue: "http://not-https.services.ai.azure.com/api/projects/p",
			EnvName:  "dev",
		}, nil),
	})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolveProjectEndpoint_GlobalConfigResolves(t *testing.T) {
	// Level 3: global config wins over FOUNDRY_PROJECT_ENDPOINT when no azd env value is set.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		ReadAzdHostedSources: stubReadAzdHostedSources(azdHostedSources{
			CfgState: projectContextState{
				Endpoint: "  HTTPS://Cfg.Services.AI.Azure.com/api/projects/p/  ",
				SetAt:    "2025-01-02T03:04:05Z",
			},
			CfgFound: true,
		}, nil),
	})
	require.NoError(t, err)
	assert.Equal(t, "https://cfg.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceGlobalConfig, result.Source)
	assert.Equal(t, "2025-01-02T03:04:05Z", result.SetAt)
}

func TestResolveProjectEndpoint_GlobalConfigInvalidRejected(t *testing.T) {
	// Level 3 invalid values are hard errors (no silent fallback to level 4).
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")

	_, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		ReadAzdHostedSources: stubReadAzdHostedSources(azdHostedSources{
			CfgState: projectContextState{
				Endpoint: "http://not-https.services.ai.azure.com/api/projects/p",
				SetAt:    "2025-01-02T03:04:05Z",
			},
			CfgFound: true,
		}, nil),
	})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolveProjectEndpoint_HostedSourcesErrorPropagates(t *testing.T) {
	// Non-recoverable errors from the hosted-source lookup (e.g. config parse
	// failure) must be surfaced and must not silently fall through to level 4.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")
	sentinel := errors.New("boom")

	_, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		ReadAzdHostedSources: stubReadAzdHostedSources(azdHostedSources{}, sentinel),
	})
	require.ErrorIs(t, err, sentinel)
}

func TestResolveProjectEndpoint_FoundryEnvFallback(t *testing.T) {
	// No flag, no azd-hosted sources → falls back to FOUNDRY_PROJECT_ENDPOINT.
	opts := isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://env.services.ai.azure.com/api/projects/env-proj")

	result, err := resolveProjectEndpoint(t.Context(), opts)
	require.NoError(t, err)
	assert.Equal(t, "https://env.services.ai.azure.com/api/projects/env-proj", result.Endpoint)
	assert.Equal(t, SourceFoundryEnv, result.Source)
}

func TestResolveProjectEndpoint_NothingResolvable(t *testing.T) {
	opts := isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")

	_, err := resolveProjectEndpoint(t.Context(), opts)
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeMissingProjectEndpoint, localErr.Code)
}

func TestResolveProjectEndpoint_InvalidFlagRejected(t *testing.T) {
	opts := isolateFromAzdDaemon(t)
	opts.FlagValue = "http://not-https.services.ai.azure.com/api/projects/p"
	_, err := resolveProjectEndpoint(t.Context(), opts)
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolveProjectEndpoint_InvalidFoundryEnvRejected(t *testing.T) {
	opts := isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "http://bad.services.ai.azure.com/api/projects/p")

	_, err := resolveProjectEndpoint(t.Context(), opts)
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolveProjectEndpoint_FoundryEnvNormalized(t *testing.T) {
	opts := isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "  https://X.SERVICES.AI.AZURE.COM/api/projects/p/  ")

	result, err := resolveProjectEndpoint(t.Context(), opts)
	require.NoError(t, err)
	assert.Equal(t, "https://x.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceFoundryEnv, result.Source)
}

func TestResolveProjectEndpoint_LegacyAgentsConfigResolves(t *testing.T) {
	// Level 3 with the new key absent, but the legacy
	// `extensions.ai-agents.project.context` key present. The resolver should
	// surface the legacy value as SourceGlobalConfig and flag the result so
	// callers can prompt the user to migrate.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		ReadAzdHostedSources: stubReadAzdHostedSources(azdHostedSources{
			CfgState: projectContextState{
				Endpoint: "https://legacy.services.ai.azure.com/api/projects/p",
				SetAt:    "2024-12-31T23:59:59Z",
			},
			CfgFound:            true,
			CfgFromLegacyAgents: true,
		}, nil),
	})
	require.NoError(t, err)
	assert.Equal(t, "https://legacy.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceGlobalConfig, result.Source)
	assert.True(t, result.FromLegacyAgentsConfig,
		"resolver must propagate the legacy flag so show can surface a migration notice")
}
