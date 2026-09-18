// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An account endpoint is what the Foundry portal shows first and what most
// people paste. It passes every other check -- https, a Foundry host, no port
// -- and then answers every data-plane request with 404, which this CLI
// reported as the dataset not existing.
//
// The validator has always noticed the shape. Resolve threw the answer away at
// all four levels, so nothing could say it. These pin that it comes back out.
func TestResolveCarriesThePathWarningFromEveryLevel(t *testing.T) {
	const account = "https://acct.services.ai.azure.com"
	const proj = "https://acct.services.ai.azure.com/api/projects/p"

	t.Run("flag", func(t *testing.T) {
		isolateFromAzdDaemon(t)

		got, err := Resolve(t.Context(), ResolveOpts{FlagValue: account})
		require.NoError(t, err, "the shape is a convention, not a rule: it must not refuse")
		assert.True(t, got.PathWarning)

		got, err = Resolve(t.Context(), ResolveOpts{FlagValue: proj})
		require.NoError(t, err)
		assert.False(t, got.PathWarning, "a project endpoint has nothing to warn about")
	})

	t.Run("azd environment", func(t *testing.T) {
		t.Setenv("AZD_SERVER", "")
		withHostedSources(t, AzdHostedSources{EnvValue: account, EnvName: "dev"}, nil)

		got, err := Resolve(t.Context(), ResolveOpts{})
		require.NoError(t, err)
		assert.Equal(t, SourceAzdEnv, got.Source)
		assert.True(t, got.PathWarning)
	})

	t.Run("global config", func(t *testing.T) {
		t.Setenv("AZD_SERVER", "")
		withHostedSources(t, AzdHostedSources{
			CfgFound: true,
			CfgState: State{Endpoint: account},
		}, nil)

		got, err := Resolve(t.Context(), ResolveOpts{})
		require.NoError(t, err)
		assert.Equal(t, SourceGlobalConfig, got.Source)
		assert.True(t, got.PathWarning)
	})

	t.Run("host environment variable", func(t *testing.T) {
		isolateFromAzdDaemon(t)
		t.Setenv("FOUNDRY_PROJECT_ENDPOINT", account)

		got, err := Resolve(t.Context(), ResolveOpts{})
		require.NoError(t, err)
		assert.Equal(t, SourceFoundryEnv, got.Source)
		assert.True(t, got.PathWarning)
	})
}

// A warning that names "globalConfig" leaves the reader looking for a file, so
// each source says where to go and change it.
func TestEndpointSourceDescribesWhereToChangeIt(t *testing.T) {
	assert.Equal(t, "--project-endpoint", SourceFlag.Describe())
	assert.Equal(t, "the azd environment", SourceAzdEnv.Describe())
	assert.Equal(t, "~/.azd/config.json", SourceGlobalConfig.Describe())
	assert.Equal(t, "the environment", SourceFoundryEnv.Describe())
	assert.Equal(t, "something-new", EndpointSource("something-new").Describe(),
		"a source added later still has to render as something")
}
