// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"azure.ai.connections/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// withHostedSources installs a stub for ReadAzdHostedSourcesFunc for the
// duration of the test and restores the production value on cleanup. Tests
// using this MUST NOT run in parallel because the seam is a package-level var.
func withHostedSources(t *testing.T, sources AzdHostedSources, err error) {
	t.Helper()
	orig := ReadAzdHostedSourcesFunc
	ReadAzdHostedSourcesFunc = func(context.Context, string) (AzdHostedSources, error) {
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

func TestResolveUsesSelectedEnvironment(t *testing.T) {
	var receivedEnvironment string
	original := ReadAzdHostedSourcesFunc
	ReadAzdHostedSourcesFunc = func(
		_ context.Context,
		environmentName string,
	) (AzdHostedSources, error) {
		receivedEnvironment = environmentName
		return AzdHostedSources{
			EnvName:  environmentName,
			EnvValue: "https://staging.services.ai.azure.com/api/projects/project",
		}, nil
	}
	t.Cleanup(func() { ReadAzdHostedSourcesFunc = original })

	resolved, err := Resolve(t.Context(), ResolveOpts{EnvironmentName: "staging"})
	require.NoError(t, err)
	assert.Equal(t, "staging", receivedEnvironment)
	assert.Equal(t, "staging", resolved.AzdEnvName)
}

func TestResolveEnvironmentIsStrict(t *testing.T) {
	const production = "https://production.services.ai.azure.com/api/projects/production"
	const staging = "https://staging.services.ai.azure.com/api/projects/staging"
	t.Setenv(foundryEnvKey, production)
	t.Setenv(azureAiEnvKey, production)
	// The normal cascade must not even be consulted during lifecycle lookup.
	original := ReadAzdHostedSourcesFunc
	ReadAzdHostedSourcesFunc = func(context.Context, string) (AzdHostedSources, error) {
		t.Error("lifecycle lookup consulted the standalone cascade")
		return AzdHostedSources{CfgFound: true, CfgState: State{Endpoint: production}}, nil
	}
	t.Cleanup(func() { ReadAzdHostedSourcesFunc = original })

	tests := []struct {
		name        string
		environment string
		values      map[string]string
		readErr     error
		want        string
		wantCode    string
	}{
		{
			name: "canonical endpoint wins", environment: "staging",
			values: map[string]string{foundryEnvKey: staging + "/", azureAiEnvKey: production}, want: staging,
		},
		{
			name: "alias in same environment", environment: "staging",
			values: map[string]string{azureAiEnvKey: staging}, want: staging,
		},
		{
			name: "missing endpoint ignores global and process", environment: "staging",
			wantCode: exterrors.CodeMissingProjectEndpoint,
		},
		{
			name: "empty endpoint ignores global and process", environment: "staging",
			values: map[string]string{foundryEnvKey: "  ", azureAiEnvKey: ""}, wantCode: exterrors.CodeMissingProjectEndpoint,
		},
		{
			name: "invalid canonical endpoint cannot fall through", environment: "staging",
			values:   map[string]string{foundryEnvKey: "http://invalid", azureAiEnvKey: staging},
			wantCode: exterrors.CodeInvalidParameter,
		},
		{
			name: "read failure cannot fall through", environment: "staging",
			readErr: status.Error(codes.Unavailable, "environment unavailable"),
		},
		{name: "empty selection is rejected", wantCode: exterrors.CodeInvalidParameter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			environment := &strictEnvironmentServer{t: t, values: tt.values, err: tt.readErr}
			server := grpc.NewServer()
			azdext.RegisterEnvironmentServiceServer(server, environment)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); _ = listener.Close() })
			t.Setenv("AZD_SERVER", listener.Addr().String())

			resolved, err := ResolveEnvironment(t.Context(), tt.environment)
			if tt.want != "" {
				require.NoError(t, err)
				assert.Equal(t, &Resolved{Endpoint: tt.want, Source: SourceAzdEnv, AzdEnvName: "staging"}, resolved)
			} else {
				require.Error(t, err)
				assert.Nil(t, resolved)
				if tt.readErr != nil {
					assert.Equal(t, codes.Unavailable, status.Code(err))
				} else {
					var localErr *azdext.LocalError
					require.ErrorAs(t, err, &localErr)
					assert.Equal(t, tt.wantCode, localErr.Code)
					if tt.wantCode == exterrors.CodeMissingProjectEndpoint {
						assert.Contains(t, localErr.Message, "staging")
						assert.Contains(t, localErr.Suggestion, "staging")
					}
				}
			}
			if tt.environment == "" {
				assert.Zero(t, environment.calls.Load())
			} else {
				assert.Equal(t, int32(1), environment.calls.Load())
			}
		})
	}
}

type strictEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	t      *testing.T
	values map[string]string
	err    error
	calls  atomic.Int32
}

func (s *strictEnvironmentServer) GetValues(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	s.calls.Add(1)
	assert.Equal(s.t, "staging", request.GetName())
	if s.err != nil {
		return nil, s.err
	}
	response := &azdext.KeyValueListResponse{}
	for key, value := range s.values {
		response.KeyValues = append(response.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return response, nil
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
