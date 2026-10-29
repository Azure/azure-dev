// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"azure.ai.toolboxes/internal/foundry"
	"azure.ai.toolboxes/internal/foundry/projectctx"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/require"
)

func TestResolveToolboxAndClientPreparesDeveloperAccess(t *testing.T) {
	const endpoint = "https://acct.services.ai.azure.com/api/projects/proj"

	prevPrepare := prepareDeveloperAccess
	prevResolve := projectctx.ReadAzdHostedSourcesFunc
	t.Cleanup(func() {
		prepareDeveloperAccess = prevPrepare
		projectctx.ReadAzdHostedSourcesFunc = prevResolve
	})

	projectctx.ReadAzdHostedSourcesFunc = func(context.Context) (projectctx.AzdHostedSources, error) {
		return projectctx.AzdHostedSources{EnvValue: endpoint, EnvName: "dev"}, nil
	}

	calls := 0
	prepareDeveloperAccess = func(
		_ context.Context, opts foundry.PrepareDeveloperAccessOptions,
	) (azcore.TokenCredential, error) {
		calls++
		require.Equal(t, endpoint, opts.Endpoint)
		require.Equal(t, "dev", opts.EnvironmentName)
		return nil, nil
	}

	_, resolved, err := resolveToolboxAndClient(t.Context(), toolboxFlags{})
	require.NoError(t, err)
	require.Equal(t, endpoint, resolved.Endpoint)
	require.Equal(t, 1, calls)

	t.Run("suppresses warning in json or no-prompt mode", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			flags  toolboxFlags
			expect bool
		}{
			{
				name:   "json output suppresses warning",
				flags:  toolboxFlags{output: "json"},
				expect: false,
			},
			{
				name:   "no-prompt suppresses warning",
				flags:  toolboxFlags{output: "table", noPrompt: true},
				expect: false,
			},
			{
				name:   "interactive table enables warning",
				flags:  toolboxFlags{output: "table", noPrompt: false},
				expect: true,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var warnFn func(string)
				prepareDeveloperAccess = func(
					_ context.Context, opts foundry.PrepareDeveloperAccessOptions,
				) (azcore.TokenCredential, error) {
					warnFn = opts.Warn
					return nil, nil
				}

				_, _, err := resolveToolboxAndClient(t.Context(), tc.flags)
				require.NoError(t, err)
				if tc.expect {
					require.NotNil(t, warnFn)
				} else {
					require.Nil(t, warnFn)
				}
			})
		}
	})
}
