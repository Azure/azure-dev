// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolateFromAzdDaemon replaces the readAzdProjectSourcesFunc seam with a
// no-op stub so tests never dial a live azd gRPC server, and restores the
// original on cleanup.
func isolateFromAzdDaemon(t *testing.T) {
	t.Helper()
	orig := readAzdProjectSourcesFunc
	readAzdProjectSourcesFunc = func(_ context.Context) (azdProjectSources, error) {
		return azdProjectSources{}, nil
	}
	t.Cleanup(func() { readAzdProjectSourcesFunc = orig })
}

// ─── isFoundryHost ────────────────────────────────────────────────────────────

func TestIsFoundryHost(t *testing.T) {
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
			assert.Equal(t, tt.want, isFoundryHost(tt.host))
		})
	}
}

// ─── validateProjectEndpoint ─────────────────────────────────────────────────

func TestValidateProjectEndpoint_ValidURLs(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  string
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
			got, err := validateProjectEndpoint(tt.raw)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateProjectEndpoint_Rejections(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty string", raw: ""},
		{name: "http scheme", raw: "http://myaccount.services.ai.azure.com/api/projects/x"},
		{name: "non-foundry host", raw: "https://management.azure.com/api/projects/x"},
		{name: "no scheme", raw: "myaccount.services.ai.azure.com/api/projects/x"},
		{name: "localhost", raw: "https://localhost/api/projects/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateProjectEndpoint(tt.raw)
			assert.Error(t, err)
		})
	}
}

// ─── resolveProjectEndpoint cascade ──────────────────────────────────────────

func TestResolveProjectEndpoint_FlagWins(t *testing.T) {
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://other.services.ai.azure.com/api/projects/other")

	got, err := resolveProjectEndpoint(t.Context(), "https://myaccount.services.ai.azure.com/api/projects/p")
	require.NoError(t, err)
	assert.Equal(t, SourceFlag, got.Source)
	assert.Equal(t, "https://myaccount.services.ai.azure.com/api/projects/p", got.Endpoint)
}

func TestResolveProjectEndpoint_AzdEnv(t *testing.T) {
	isolateFromAzdDaemon(t)

	readAzdProjectSourcesFunc = func(_ context.Context) (azdProjectSources, error) {
		return azdProjectSources{
			EnvValue: "https://envaccount.services.ai.azure.com/api/projects/env",
			EnvName:  "my-env",
		}, nil
	}
	t.Cleanup(func() { isolateFromAzdDaemon(t) }) // not strictly needed; isolate cleans up

	got, err := resolveProjectEndpoint(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, SourceAzdEnv, got.Source)
	assert.Equal(t, "https://envaccount.services.ai.azure.com/api/projects/env", got.Endpoint)
}

func TestResolveProjectEndpoint_GlobalConfig(t *testing.T) {
	isolateFromAzdDaemon(t)

	readAzdProjectSourcesFunc = func(_ context.Context) (azdProjectSources, error) {
		return azdProjectSources{
			CfgEndpoint: "https://cfgaccount.services.ai.azure.com/api/projects/cfg",
			CfgFound:    true,
		}, nil
	}

	got, err := resolveProjectEndpoint(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, SourceGlobalConfig, got.Source)
	assert.Equal(t, "https://cfgaccount.services.ai.azure.com/api/projects/cfg", got.Endpoint)
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
	assert.Error(t, err)
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
