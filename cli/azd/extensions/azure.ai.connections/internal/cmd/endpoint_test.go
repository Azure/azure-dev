// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveARMContext_PrefersEnvProjectID verifies that a matching
// AZURE_AI_PROJECT_ID resolves the ARM context without any data-plane call, so
// the first connection can be created on a project that has none yet. The nil
// dpClient asserts that discovery is not reached on this path.
func TestResolveARMContext_PrefersEnvProjectID(t *testing.T) {
	t.Parallel()

	projectID := "/subscriptions/sub-123/resourceGroups/rg-abc/providers/" +
		"Microsoft.CognitiveServices/accounts/cog-xyz/projects/proj-1"

	armCtx, err := resolveARMContext(t.Context(), projectID, "cog-xyz", "proj-1", nil)
	require.NoError(t, err)
	assert.Equal(t, "sub-123", armCtx.SubscriptionID)
	assert.Equal(t, "rg-abc", armCtx.ResourceGroup)
	assert.Equal(t, "cog-xyz", armCtx.AccountName)
	assert.Equal(t, "proj-1", armCtx.ProjectName)
}

// TestResolveARMContext_MatchIsCaseInsensitive verifies the account/project
// guard tolerates casing differences between the endpoint host and the ARM
// resource ID, which are case-insensitive in Azure.
func TestResolveARMContext_MatchIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	projectID := "/subscriptions/sub-123/resourceGroups/rg-abc/providers/" +
		"Microsoft.CognitiveServices/accounts/Cog-XYZ/projects/Proj-1"

	armCtx, err := resolveARMContext(t.Context(), projectID, "cog-xyz", "proj-1", nil)
	require.NoError(t, err)
	assert.Equal(t, "rg-abc", armCtx.ResourceGroup)
}
