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
// might be reachable on the developer machine via AZD_SERVER. It clears
// AZD_SERVER and stubs readAzdProjectSourcesFunc to return no project sources,
// so the resolver under test only sees the flag and FOUNDRY_PROJECT_ENDPOINT.
func isolateFromAzdDaemon(t *testing.T) {
	t.Helper()
	t.Setenv("AZD_SERVER", "")
	stubAzdProjectSources(t, azdProjectSources{}, nil)
}

func TestResolveProjectEndpoint_FlagWins(t *testing.T) {
	// Even with FOUNDRY_PROJECT_ENDPOINT and azd-hosted sources set, the flag wins.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://other.example.com")
	stubAzdProjectSources(t, azdProjectSources{
		EnvValue: "https://envaccount.example.com",
	}, nil)

	ep, src, err := resolveProjectEndpoint(t.Context(), "https://flag.example.com")
	require.NoError(t, err)
	assert.Equal(t, "https://flag.example.com", ep)
	assert.Equal(t, sourceFlag, src)
}

func TestResolveProjectEndpoint_AzdEnvBeatsGlobalConfig(t *testing.T) {
	isolateFromAzdDaemon(t)
	stubAzdProjectSources(t, azdProjectSources{
		EnvValue:             "https://envaccount.example.com",
		CfgEndpoint:          "https://cfg.example.com",
		CfgFound:             true,
		LegacySkillsEndpoint: "https://legacy-skills.example.com",
		LegacySkillsFound:    true,
		LegacyAgentsEndpoint: "https://legacy-agents.example.com",
		LegacyAgentsFound:    true,
	}, nil)

	ep, src, err := resolveProjectEndpoint(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, "https://envaccount.example.com", ep)
	assert.Equal(t, sourceAzdEnv, src)
}

func TestResolveProjectEndpoint_GlobalConfig_PrefersAiProjects(t *testing.T) {
	isolateFromAzdDaemon(t)
	stubAzdProjectSources(t, azdProjectSources{
		CfgEndpoint:          "https://new.example.com",
		CfgFound:             true,
		LegacySkillsEndpoint: "https://legacy-skills.example.com",
		LegacySkillsFound:    true,
		LegacyAgentsEndpoint: "https://legacy-agents.example.com",
		LegacyAgentsFound:    true,
	}, nil)

	ep, src, err := resolveProjectEndpoint(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, "https://new.example.com", ep,
		"the new ai-projects.context key must win over both legacy paths")
	assert.Equal(t, sourceGlobalConfig, src)
}

func TestResolveProjectEndpoint_GlobalConfig_FallsBackToLegacySkills(t *testing.T) {
	isolateFromAzdDaemon(t)
	stubAzdProjectSources(t, azdProjectSources{
		LegacySkillsEndpoint: "https://legacy-skills.example.com",
		LegacySkillsFound:    true,
		LegacyAgentsEndpoint: "https://legacy-agents.example.com",
		LegacyAgentsFound:    true,
	}, nil)

	ep, src, err := resolveProjectEndpoint(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, "https://legacy-skills.example.com", ep,
		"with no ai-projects hit, the ai-skills legacy key wins over ai-agents")
	assert.Equal(t, sourceGlobalConfig, src)
}

func TestResolveProjectEndpoint_GlobalConfig_FallsBackToLegacyAgents(t *testing.T) {
	isolateFromAzdDaemon(t)
	stubAzdProjectSources(t, azdProjectSources{
		LegacyAgentsEndpoint: "https://legacy-agents.example.com",
		LegacyAgentsFound:    true,
	}, nil)

	ep, src, err := resolveProjectEndpoint(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, "https://legacy-agents.example.com", ep)
	assert.Equal(t, sourceGlobalConfig, src)
}

func TestResolveProjectEndpoint_HostEnvVar(t *testing.T) {
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://host.example.com")

	ep, src, err := resolveProjectEndpoint(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, "https://host.example.com", ep)
	assert.Equal(t, sourceFoundryEnv, src)
}

func TestResolveProjectEndpoint_InvalidScheme(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		wantMsg  string
	}{
		{"http scheme", "http://example.com", "must use https scheme"},
		{"no scheme", "example.com/foo", "must use https scheme"},
		{"empty host", "https:///path", "missing host"},
		{"ftp scheme", "ftp://example.com", "must use https scheme"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := resolveProjectEndpoint(context.Background(), tc.endpoint)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

func TestResolveProjectEndpoint_MissingAll(t *testing.T) {
	isolateFromAzdDaemon(t)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")

	_, _, err := resolveProjectEndpoint(t.Context(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no Foundry project endpoint resolved")

	var le *azdext.LocalError
	require.True(t, errors.As(err, &le), "expected a LocalError")
	assert.Contains(t, le.Suggestion, "azd ai project set",
		"the dependency-error suggestion should point at the supported `azd ai project set` command")
}
