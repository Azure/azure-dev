// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"azureaiskills/internal/foundry"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/require"
)

func TestResolveSkillContextPreparesDeveloperAccess(t *testing.T) {
	const endpoint = "https://acct.services.ai.azure.com/api/projects/proj"

	prevPrepare := prepareDeveloperAccess
	t.Cleanup(func() { prepareDeveloperAccess = prevPrepare })

	calls := 0
	prepareDeveloperAccess = func(
		_ context.Context, opts foundry.PrepareDeveloperAccessOptions,
	) (azcore.TokenCredential, error) {
		calls++
		require.Equal(t, endpoint, opts.Endpoint)
		require.Equal(t, "dev", opts.EnvironmentName)
		return nil, nil
	}

	sc, err := resolveSkillContext(t.Context(), endpoint, "dev")
	require.NoError(t, err)
	require.Equal(t, endpoint, sc.endpoint)
	require.Equal(t, 1, calls)
}

func TestResolveSkillContextWithOptions_WarningRouting(t *testing.T) {
	const endpoint = "https://acct.services.ai.azure.com/api/projects/proj"

	prevPrepare := prepareDeveloperAccess
	t.Cleanup(func() { prepareDeveloperAccess = prevPrepare })

	t.Run("routes warning to progress reporter during deploy", func(t *testing.T) {
		var progressed string
		prepareDeveloperAccess = func(
			_ context.Context, opts foundry.PrepareDeveloperAccessOptions,
		) (azcore.TokenCredential, error) {
			require.NotNil(t, opts.Warn)
			opts.Warn("auto-assign denied")
			return nil, nil
		}

		_, err := resolveSkillContextWithOptions(t.Context(), skillContextOptions{
			flagEndpoint:    endpoint,
			environmentName: "dev",
			progressReporter: func(msg string) {
				progressed = msg
			},
		})
		require.NoError(t, err)
		require.Equal(t, "Warning: auto-assign denied", progressed)
	})

	t.Run("suppresses warning in json output or no-prompt mode", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			opts   skillContextOptions
			expect bool
		}{
			{
				name:   "json output suppresses warning",
				opts:   skillContextOptions{flagEndpoint: endpoint, outputFormat: outputJSON},
				expect: false,
			},
			{
				name:   "no-prompt suppresses warning",
				opts:   skillContextOptions{flagEndpoint: endpoint, outputFormat: outputTable, noPrompt: true},
				expect: false,
			},
			{
				name:   "interactive table enables warning",
				opts:   skillContextOptions{flagEndpoint: endpoint, outputFormat: outputTable, noPrompt: false},
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

				_, err := resolveSkillContextWithOptions(t.Context(), tc.opts)
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
