// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stubAzdHostedSources replaces readAzdHostedSourcesFunc for the duration of
// the test with a function that returns the given sources/err.
func stubAzdHostedSources(t *testing.T, sources azdHostedSources, err error) {
	t.Helper()
	orig := readAzdHostedSourcesFunc
	readAzdHostedSourcesFunc = func(context.Context, resolveProjectEndpointOpts) (azdHostedSources, error) {
		return sources, err
	}
	t.Cleanup(func() { readAzdHostedSourcesFunc = orig })
}

// isolateFromAzdDaemon makes the test independent of any azd daemon that
// might be reachable on the developer machine via AZD_SERVER. It does two
// things:
//   - Clears AZD_SERVER so azdext.NewAzdClient() cannot connect.
//   - Stubs readAzdHostedSourcesFunc to return no hosted sources.
//
// Together this ensures the resolver under test only sees the flag and the
// FOUNDRY_PROJECT_ENDPOINT host env var.
func isolateFromAzdDaemon(t *testing.T) {
	t.Helper()
	t.Setenv("AZD_SERVER", "")
	stubAzdHostedSources(t, azdHostedSources{}, nil)
}

func TestResolveProjectEndpoint_FlagWins(t *testing.T) {
	// Even with FOUNDRY_PROJECT_ENDPOINT and azd-hosted sources set, the flag should win.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://env.services.ai.azure.com/api/projects/env-proj")
	stubAzdHostedSources(t, azdHostedSources{
		EnvValue: "https://azdenv.services.ai.azure.com/api/projects/p",
		EnvName:  "dev",
	}, nil)

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		FlagValue: "https://flag.services.ai.azure.com/api/projects/flag-proj",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://flag.services.ai.azure.com/api/projects/flag-proj", result.Endpoint)
	assert.Equal(t, SourceFlag, result.Source)
}

func TestResolveProjectEndpoint_AzdEnvResolves(t *testing.T) {
	// Level 2: FOUNDRY_PROJECT_ENDPOINT from the active azd env wins over
	// global config and FOUNDRY_PROJECT_ENDPOINT.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")
	stubAzdHostedSources(t, azdHostedSources{
		EnvValue: "  HTTPS://Azdenv.Services.AI.Azure.com/api/projects/p/  ",
		EnvName:  "dev",
		CfgState: projectContextState{
			Endpoint: "https://cfg.services.ai.azure.com/api/projects/p",
			SetAt:    "2025-01-01T00:00:00Z",
		},
		CfgFound: true,
	}, nil)

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://azdenv.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceAzdEnv, result.Source)
	assert.Equal(t, "dev", result.AzdEnvName)
}

func TestResolveProjectEndpoint_UsesRequestedAzdEnvironment(t *testing.T) {
	var requestedEnv string
	orig := readAzdHostedSourcesFunc
	readAzdHostedSourcesFunc = func(_ context.Context, opts resolveProjectEndpointOpts) (azdHostedSources, error) {
		requestedEnv = opts.EnvName
		return azdHostedSources{
			EnvValue: "https://staging.services.ai.azure.com/api/projects/p",
			EnvName:  opts.EnvName,
		}, nil
	}
	t.Cleanup(func() { readAzdHostedSourcesFunc = orig })

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{EnvName: "staging"})

	require.NoError(t, err)
	assert.Equal(t, "staging", requestedEnv)
	assert.Equal(t, "https://staging.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, "staging", result.AzdEnvName)
}

func TestResolveProjectEndpoint_EnvironmentBoundLookup(t *testing.T) {
	for _, tc := range []struct {
		name      string
		envName   string
		endpoint  string
		readError bool
		wantError string
	}{
		{name: "selected", envName: "staging", endpoint: "https://staging.services.ai.azure.com/api/projects/p"},
		{name: "current", endpoint: "https://dev.services.ai.azure.com/api/projects/p"},
		{name: "missing selected value", envName: "staging", wantError: "no FOUNDRY_PROJECT_ENDPOINT"},
		{name: "missing current value", wantError: "no FOUNDRY_PROJECT_ENDPOINT"},
		{name: "missing environment", envName: "missing", wantError: "getting azd environment"},
		{name: "value read failure", envName: "staging", readError: true, wantError: "reading FOUNDRY_PROJECT_ENDPOINT"},
		{name: "invalid value", envName: "staging", endpoint: "http://invalid", wantError: "https"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := tc.envName
			if selected == "" {
				selected = "dev"
			}
			env := testEnvironmentServiceServer{
				current: &azdext.Environment{Name: "dev"},
				environments: map[string]*azdext.Environment{
					"staging": {Name: "staging"},
				},
				values: map[string]map[string]string{
					selected: {"FOUNDRY_PROJECT_ENDPOINT": tc.endpoint},
				},
			}
			var envServer azdext.EnvironmentServiceServer = &env
			if tc.readError {
				envServer = &helpersFailingEnvironmentServer{
					testEnvironmentServiceServer: env,
					getValueErr:                  status.Error(codes.Unavailable, "read failed"),
				}
			}
			address := newTestAzdServer(t, envServer, &testWorkflowServiceServer{})
			t.Setenv("AZD_SERVER", address)
			t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://shell.services.ai.azure.com/api/projects/other")

			resolved, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
				EnvName: tc.envName, RequireEnvironmentEndpoint: true,
			})

			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Nil(t, resolved)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.endpoint, resolved.Endpoint)
				assert.Equal(t, selected, resolved.AzdEnvName)
			}
		})
	}
}

func TestResolveProjectEndpoint_AzdEnvInvalidRejected(t *testing.T) {
	// Level 2 invalid values are hard errors (no silent fallback to lower levels).
	// FOUNDRY_PROJECT_ENDPOINT is set, but resolver must NOT fall through to it.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")
	stubAzdHostedSources(t, azdHostedSources{
		EnvValue: "http://not-https.services.ai.azure.com/api/projects/p",
		EnvName:  "dev",
	}, nil)

	_, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolveProjectEndpoint_GlobalConfigResolves(t *testing.T) {
	// Level 3: global config wins over FOUNDRY_PROJECT_ENDPOINT when no azd env value is set.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")
	stubAzdHostedSources(t, azdHostedSources{
		CfgState: projectContextState{
			Endpoint: "  HTTPS://Cfg.Services.AI.Azure.com/api/projects/p/  ",
			SetAt:    "2025-01-02T03:04:05Z",
		},
		CfgFound: true,
	}, nil)

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://cfg.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceGlobalConfig, result.Source)
	assert.Equal(t, "2025-01-02T03:04:05Z", result.SetAt)
}

func TestResolveProjectEndpoint_GlobalConfigInvalidRejected(t *testing.T) {
	// Level 3 invalid values are hard errors (no silent fallback to level 4).
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.services.ai.azure.com/api/projects/p")
	stubAzdHostedSources(t, azdHostedSources{
		CfgState: projectContextState{
			Endpoint: "http://not-https.services.ai.azure.com/api/projects/p",
			SetAt:    "2025-01-02T03:04:05Z",
		},
		CfgFound: true,
	}, nil)

	_, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
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
	stubAzdHostedSources(t, azdHostedSources{}, sentinel)

	_, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
	require.ErrorIs(t, err, sentinel)
}

func TestResolveProjectEndpoint_FoundryEnvFallback(t *testing.T) {
	// No flag, no azd-hosted sources → falls back to FOUNDRY_PROJECT_ENDPOINT.
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://env.services.ai.azure.com/api/projects/env-proj")

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://env.services.ai.azure.com/api/projects/env-proj", result.Endpoint)
	assert.Equal(t, SourceFoundryEnv, result.Source)
}

func TestResolveProjectEndpoint_NothingResolvable(t *testing.T) {
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")

	_, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeMissingProjectEndpoint, localErr.Code)
}

func TestResolveProjectEndpoint_InvalidFlagRejected(t *testing.T) {
	isolateFromAzdDaemon(t)
	_, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		FlagValue: "http://not-https.services.ai.azure.com/api/projects/p",
	})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolveProjectEndpoint_InvalidFoundryEnvRejected(t *testing.T) {
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "http://bad.services.ai.azure.com/api/projects/p")

	_, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Contains(t, localErr.Message, "https")
}

func TestResolveProjectEndpoint_FoundryEnvNormalized(t *testing.T) {
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "  https://X.SERVICES.AI.AZURE.COM/api/projects/p/  ")

	result, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{})
	require.NoError(t, err)
	assert.Equal(t, "https://x.services.ai.azure.com/api/projects/p", result.Endpoint)
	assert.Equal(t, SourceFoundryEnv, result.Source)
}
