// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	foundryaccess "azure.ai.toolboxes/internal/accesshost"
	"azure.ai.toolboxes/internal/foundry/projectctx"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/require"
)

func TestToolboxReadAndMutationAccess(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	previousPrepare, previousResolve := prepareFoundryAccess, projectctx.ReadAzdHostedSourcesFunc
	t.Cleanup(func() {
		prepareFoundryAccess, projectctx.ReadAzdHostedSourcesFunc = previousPrepare, previousResolve
	})
	projectctx.ReadAzdHostedSourcesFunc = func(context.Context) (projectctx.AzdHostedSources, error) {
		return projectctx.AzdHostedSources{EnvValue: endpoint, EnvName: "staging"}, nil
	}
	calls := 0
	prepareFoundryAccess = func(_ context.Context, options foundryaccess.PrepareOptions) (azcore.TokenCredential, error) {
		calls++
		require.Equal(t, endpoint, options.Endpoint)
		require.Equal(t, "staging", options.EnvironmentName)
		require.True(t, options.AutoGrant)
		return nil, nil
	}
	_, _, err := resolveToolboxAndClient(t.Context(), toolboxFlags{environment: "staging"})
	require.NoError(t, err)
	require.Zero(t, calls, "read-only commands must not grant roles")
	_, _, err = resolveToolboxAndClient(t.Context(), toolboxFlags{environment: "staging"}, true)
	require.NoError(t, err)
	require.Equal(t, 1, calls, "standalone mutations must prepare access without agent hooks")
	prepareFoundryAccess = func(context.Context, foundryaccess.PrepareOptions) (azcore.TokenCredential, error) {
		return nil, context.Canceled
	}
	_, _, err = resolveToolboxAndClient(t.Context(), toolboxFlags{}, true)
	require.ErrorIs(t, err, context.Canceled)
}
