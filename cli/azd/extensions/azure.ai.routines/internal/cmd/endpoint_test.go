// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubAzdProjectSources replaces readAzdProjectSourcesFunc for the duration of
// the test with a function that returns the given sources/err.
func stubAzdProjectSources(t *testing.T, sources azdProjectSources, err error) {
	t.Helper()
	orig := readAzdProjectSourcesFunc
	readAzdProjectSourcesFunc = func(context.Context) (azdProjectSources, error) {
		return sources, err
	}
	t.Cleanup(func() { readAzdProjectSourcesFunc = orig })
}

// isolateFromAzdDaemon makes the test independent of any azd daemon that
// might be reachable on the developer machine via AZD_SERVER. It does two
// things:
//   - Clears AZD_SERVER so azdext.NewAzdClient() cannot connect.
//   - Stubs readAzdProjectSourcesFunc to return no project sources.
//
// Together this ensures the resolver under test only sees the flag and the
// FOUNDRY_PROJECT_ENDPOINT host env var.
func isolateFromAzdDaemon(t *testing.T) {
	t.Helper()
	t.Setenv("AZD_SERVER", "")
	stubAzdProjectSources(t, azdProjectSources{}, nil)
}

// ─── isFoundryHost ────────────────────────────────────────────────────────────

func TestIsFoundryHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		host string
		want bool
	}{
		{"myaccount.services.ai.azure.com", true},
		{"myaccount.SERVICES.AI.AZURE.COM", true}, // case-insensitive
		{"sub.myaccount.services.ai.azure.com", true},
		{"evil.example.com", false},
		{"services.ai.azure.com.evil.com", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isFoundryHost(tt.host))
		})
	}
}

// ─── validateProjectEndpoint ─────────────────────────────────────────────────

func TestValidateProjectEndpoint_ValidURLs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "basic endpoint",
			raw:  "https://myaccount.services.ai.azure.com/api/projects/myproj",
			want: "https://myaccount.services.ai.azure.com/api/projects/myproj",
		},
		{
			name: "uppercase scheme",
			raw:  "HTTPS://MyAccount.SERVICES.AI.AZURE.COM/api/projects/myproj",
			want: "https://myaccount.services.ai.azure.com/api/projects/myproj",
		},
		{
			name: "trailing slash stripped",
			raw:  "https://myaccount.services.ai.azure.com/api/projects/myproj/",
			want: "https://myaccount.services.ai.azure.com/api/projects/myproj",
		},
		{
			name: "host only (no path)",
			raw:  "https://myaccount.services.ai.azure.com",
			want: "https://myaccount.services.ai.azure.com",
		},
		{
			name: "leading/trailing whitespace trimmed",
			raw:  "  https://myaccount.services.ai.azure.com/api/projects/x  ",
			want: "https://myaccount.services.ai.azure.com/api/projects/x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := validateProjectEndpoint(tt.raw)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateProjectEndpoint_Rejections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty string", raw: ""},
		{name: "http scheme", raw: "http://myaccount.services.ai.azure.com/api/projects/x"},
		{name: "non-foundry host", raw: "https://management.azure.com/api/projects/x"},
		{name: "no scheme", raw: "myaccount.services.ai.azure.com/api/projects/x"},
		{name: "localhost", raw: "https://localhost/api/projects/x"},
		{name: "explicit port", raw: "https://myaccount.services.ai.azure.com:444/api/projects/x"},
		{name: "default port still rejected", raw: "https://myaccount.services.ai.azure.com:443/api/projects/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := validateProjectEndpoint(tt.raw)
			assert.Error(t, err)
		})
	}
}

// ─── resolveProjectEndpoint cascade ──────────────────────────────────────────

func TestResolveProjectEndpoint_FlagWins(t *testing.T) {
	// Even with FOUNDRY_PROJECT_ENDPOINT and azd-hosted sources set, the flag should win.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://other.services.ai.azure.com/api/projects/other")
	stubAzdProjectSources(t, azdProjectSources{
		EnvValue: "https://envaccount.services.ai.azure.com/api/projects/env",
		EnvName:  "my-env",
	}, nil)

	got, err := resolveProjectEndpoint(t.Context(), "https://myaccount.services.ai.azure.com/api/projects/p")
	require.NoError(t, err)
	assert.Equal(t, SourceFlag, got.Source)
	assert.Equal(t, "https://myaccount.services.ai.azure.com/api/projects/p", got.Endpoint)
}

func TestResolveProjectEndpoint_AzdEnv(t *testing.T) {
	isolateFromAzdDaemon(t)
	stubAzdProjectSources(t, azdProjectSources{
		EnvValue: "https://envaccount.services.ai.azure.com/api/projects/env",
		EnvName:  "my-env",
	}, nil)

	got, err := resolveProjectEndpoint(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, SourceAzdEnv, got.Source)
	assert.Equal(t, "https://envaccount.services.ai.azure.com/api/projects/env", got.Endpoint)
}

func TestResolveProjectEndpoint_GlobalConfig(t *testing.T) {
	isolateFromAzdDaemon(t)
	stubAzdProjectSources(t, azdProjectSources{
		CfgEndpoint: "https://cfgaccount.services.ai.azure.com/api/projects/cfg",
		CfgFound:    true,
	}, nil)

	got, err := resolveProjectEndpoint(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, SourceGlobalConfig, got.Source)
	assert.Equal(t, "https://cfgaccount.services.ai.azure.com/api/projects/cfg", got.Endpoint)
}

func TestResolveProjectEndpoint_GlobalConfig_PrefersAiProjectsOverLegacyAgents(t *testing.T) {
	isolateFromAzdDaemon(t)
	stubAzdProjectSources(t, azdProjectSources{
		CfgEndpoint:          "https://new.services.ai.azure.com/api/projects/new",
		CfgFound:             true,
		LegacyAgentsEndpoint: "https://legacy.services.ai.azure.com/api/projects/legacy",
		LegacyAgentsFound:    true,
	}, nil)

	got, err := resolveProjectEndpoint(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, SourceGlobalConfig, got.Source)
	assert.Equal(t, "https://new.services.ai.azure.com/api/projects/new", got.Endpoint,
		"the new ai-projects.context key must win over legacy ai-agents.project.context")
}

func TestResolveProjectEndpoint_GlobalConfig_FallsBackToLegacyAgents(t *testing.T) {
	isolateFromAzdDaemon(t)
	stubAzdProjectSources(t, azdProjectSources{
		LegacyAgentsEndpoint: "https://legacy.services.ai.azure.com/api/projects/legacy",
		LegacyAgentsFound:    true,
	}, nil)

	got, err := resolveProjectEndpoint(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, SourceGlobalConfig, got.Source,
		"a legacy-only hit still surfaces as globalConfig source")
	assert.Equal(t, "https://legacy.services.ai.azure.com/api/projects/legacy", got.Endpoint)
}

func TestResolveProjectEndpoint_FoundryEnv(t *testing.T) {
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://fenv.services.ai.azure.com/api/projects/fe")

	got, err := resolveProjectEndpoint(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, SourceFoundryEnv, got.Source)
	assert.Equal(t, "https://fenv.services.ai.azure.com/api/projects/fe", got.Endpoint)
}

func TestResolveProjectEndpoint_NoSourceReturnsError(t *testing.T) {
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")

	_, err := resolveProjectEndpoint(t.Context(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no Foundry project endpoint resolved")

	var le *azdext.LocalError
	require.True(t, errors.As(err, &le), "expected a LocalError")
	assert.Contains(t, le.Suggestion, "azd ai project set",
		"the dependency-error suggestion should point at the supported `azd ai project set` command")
}

func TestResolveProjectEndpoint_FlagNormalizesURL(t *testing.T) {
	isolateFromAzdDaemon(t)

	got, err := resolveProjectEndpoint(t.Context(),
		"HTTPS://MyAccount.SERVICES.AI.AZURE.COM/api/projects/p/")
	require.NoError(t, err)
	assert.Equal(t, SourceFlag, got.Source)
	assert.Equal(t, "https://myaccount.services.ai.azure.com/api/projects/p", got.Endpoint)
}

func TestResolveProjectEndpoint_InvalidFlagReturnsError(t *testing.T) {
	isolateFromAzdDaemon(t)

	_, err := resolveProjectEndpoint(t.Context(), "http://myaccount.services.ai.azure.com/api/projects/p")
	assert.Error(t, err, "http:// scheme should be rejected")
}
