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
