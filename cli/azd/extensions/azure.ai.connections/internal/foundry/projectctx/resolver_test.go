// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"context"
	"errors"
	"testing"

	"azure.ai.connections/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withHostedSources installs a stub for ReadAzdHostedSourcesFunc for the
// duration of the test and restores the production value on cleanup. Tests
// using this MUST NOT run in parallel because the seam is a package-level var.
func withHostedSources(t *testing.T, sources AzdHostedSources, err error) {
	t.Helper()
	orig := ReadAzdHostedSourcesFunc
	ReadAzdHostedSourcesFunc = func(context.Context) (AzdHostedSources, error) {
		return sources, err
	}
	t.Cleanup(func() { ReadAzdHostedSourcesFunc = orig })
}

// isolateFromAzdDaemon installs an empty hosted-sources stub and clears
// AZD_SERVER so any code path that bypasses the seam cannot reach a real
// daemon. After calling this, the resolver only sees the flag and the
// FOUNDRY_PROJECT_ENDPOINT host env var.
func isolateFromAzdDaemon(t *testing.T) {
	t.Helper()
	t.Setenv("AZD_SERVER", "")
	withHostedSources(t, AzdHostedSources{}, nil)
}

func TestResolve_FlagWins(t *testing.T) {
	// Even with FOUNDRY_PROJECT_ENDPOINT and azd-hosted sources set, the flag wins.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://env.services.ai.azure.com/api/projects/env-proj")
	withHostedSources(t, AzdHostedSources{
		EnvValue: "https://azdenv.services.ai.azure.com/api/projects/p",
		EnvName:  "dev",
	}, nil)

	result, err := Resolve(t.Context(), ResolveOpts{
		FlagValue: "https://flag.services.ai.azure.com/api/projects/flag-proj",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://flag.services.ai.azure.com/api/projects/flag-proj", result.Endpoint)
	assert.Equal(t, SourceFlag, result.Source)
}

func TestResolve_AzdEnvWinsOverConfigAndFoundryEnv(t *testing.T) {
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")
	withHostedSources(t, AzdHostedSources{
		EnvValue: "  HTTPS://Azdenv.Services.AI.Azure.com/api/projects/p/  ",
		EnvName:  "dev",
		CfgState: State{
			Endpoint: "https://cfg.services.ai.azure.com/api/projects/p",
			SetAt:    "2025-01-01T00:00:00Z",
		},
		CfgFound: true,
	}, nil)

	result, err := Resolve(t.Context(), ResolveOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://azdenv.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceAzdEnv, result.Source)
	assert.Equal(t, "dev", result.AzdEnvName)
}

func TestResolve_AzdEnvInvalidIsHardError(t *testing.T) {
	// Level 2 invalid values are hard errors (no silent fallback to lower levels).
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")
	withHostedSources(t, AzdHostedSources{
		EnvValue: "http://not-https.services.ai.azure.com/api/projects/p",
		EnvName:  "dev",
	}, nil)

	_, err := Resolve(t.Context(), ResolveOpts{})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolve_GlobalConfigWinsOverFoundryEnv(t *testing.T) {
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")
	withHostedSources(t, AzdHostedSources{
		CfgState: State{
			Endpoint: "  HTTPS://Cfg.Services.AI.Azure.com/api/projects/p/  ",
			SetAt:    "2025-01-02T03:04:05Z",
		},
		CfgFound: true,
	}, nil)

	result, err := Resolve(t.Context(), ResolveOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://cfg.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceGlobalConfig, result.Source)
	assert.Equal(t, "2025-01-02T03:04:05Z", result.SetAt)
}

func TestResolve_GlobalConfigInvalidIsHardError(t *testing.T) {
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")
	withHostedSources(t, AzdHostedSources{
		CfgState: State{
			Endpoint: "http://not-https.services.ai.azure.com/api/projects/p",
			SetAt:    "2025-01-02T03:04:05Z",
		},
		CfgFound: true,
	}, nil)

	_, err := Resolve(t.Context(), ResolveOpts{})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolve_HostedSourcesErrorPropagates(t *testing.T) {
	// Non-recoverable errors from the hosted-source lookup must be surfaced
	// and must not silently fall through to level 4.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")
	sentinel := errors.New("boom")
	withHostedSources(t, AzdHostedSources{}, sentinel)

	_, err := Resolve(t.Context(), ResolveOpts{})
	require.ErrorIs(t, err, sentinel)
}

func TestResolve_FoundryEnvFallback(t *testing.T) {
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://env.services.ai.azure.com/api/projects/env-proj")

	result, err := Resolve(t.Context(), ResolveOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://env.services.ai.azure.com/api/projects/env-proj", result.Endpoint)
	assert.Equal(t, SourceFoundryEnv, result.Source)
}

func TestResolve_AzureAiHostEnvFallback(t *testing.T) {
	// When FOUNDRY_PROJECT_ENDPOINT is unset, the resolver falls back to the
	// AZURE_AI_PROJECT_ENDPOINT host env var (the key azd ai agent init / azd
	// add persist). See https://github.com/Azure/azure-dev/issues/8688.
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", "https://azureai.services.ai.azure.com/api/projects/p")

	result, err := Resolve(t.Context(), ResolveOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://azureai.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceFoundryEnv, result.Source)
}

func TestResolve_FoundryHostEnvWinsOverAzureAi(t *testing.T) {
	// With both host env vars set, FOUNDRY_PROJECT_ENDPOINT takes precedence.
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/f")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", "https://azureai.services.ai.azure.com/api/projects/a")

	result, err := Resolve(t.Context(), ResolveOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://foundry.services.ai.azure.com/api/projects/f", result.Endpoint)
	assert.Equal(t, SourceFoundryEnv, result.Source)
}

func TestResolve_InvalidAzureAiHostEnvRejected(t *testing.T) {
	// An invalid AZURE_AI_PROJECT_ENDPOINT fallback is a hard error, not a
	// silent skip to level 5.
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", "http://not-https.services.ai.azure.com/api/projects/p")

	_, err := Resolve(t.Context(), ResolveOpts{})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolve_FoundryEnvNormalized(t *testing.T) {
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "  https://X.SERVICES.AI.AZURE.COM/api/projects/p/  ")

	result, err := Resolve(t.Context(), ResolveOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://x.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceFoundryEnv, result.Source)
}

func TestResolve_InvalidFlagRejected(t *testing.T) {
	isolateFromAzdDaemon(t)

	_, err := Resolve(t.Context(), ResolveOpts{
		FlagValue: "http://not-https.services.ai.azure.com/api/projects/p",
	})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolve_InvalidFoundryEnvRejected(t *testing.T) {
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "http://bad.services.ai.azure.com/api/projects/p")

	_, err := Resolve(t.Context(), ResolveOpts{})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolve_NothingResolvable(t *testing.T) {
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", "")

	_, err := Resolve(t.Context(), ResolveOpts{})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeMissingProjectEndpoint, localErr.Code)
	assert.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
}

func TestResolve_CfgFoundButEndpointEmptyFallsThrough(t *testing.T) {
	// CfgFound=true with Endpoint="" must not short-circuit; the resolver
	// should continue to level 4 (FOUNDRY_PROJECT_ENDPOINT).
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://env.services.ai.azure.com/api/projects/p")
	withHostedSources(t, AzdHostedSources{
		CfgState: State{Endpoint: "", SetAt: "2025-01-01T00:00:00Z"},
		CfgFound: true,
	}, nil)

	result, err := Resolve(t.Context(), ResolveOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://env.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceFoundryEnv, result.Source)
}

func TestContainsGRPCCode_NonGRPCErrorReturnsFalse(t *testing.T) {
	t.Parallel()
	assert.False(t, containsGRPCCode(errors.New("plain"), 0))
	assert.False(t, containsGRPCCode(nil, 0))
}
