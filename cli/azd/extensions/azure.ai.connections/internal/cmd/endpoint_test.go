// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/pkg/connections"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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

func TestResolveARMContext_EmptyProjectRequiresResourceID(t *testing.T) {
	t.Parallel()

	const endpoint = "https://secret-user:secret-password@private-account.services.ai.azure.com/" +
		"api/projects/private-project?sig=secret-signature#secret-fragment"
	account, project, err := parseEndpointComponents(endpoint)
	require.NoError(t, err)
	lister := &endpointConnectionLister{}

	armCtx, err := resolveARMContext(t.Context(), "", account, project, lister)
	require.Error(t, err)
	assert.Nil(t, armCtx)
	assert.Equal(t, 1, lister.calls)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
	assert.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
	assert.Contains(t, localErr.Message, "AZURE_AI_PROJECT_ID is required")
	assert.Contains(t, localErr.Message, "project has no connections")
	assert.Contains(t, localErr.Suggestion, "full ARM resource ID of the project matching the endpoint")
	assert.Contains(t, localErr.Suggestion, "selected azd environment (not only in the shell)")
	assert.Contains(t, localErr.Suggestion,
		"azd env set AZURE_AI_PROJECT_ID \"<project-resource-id>\" --environment \"<environment>\"")
	assert.Contains(t, localErr.Suggestion,
		"azd ai project add --project-id \"<project-resource-id>\" --environment \"<environment>\"")
	assert.Contains(t, localErr.Suggestion, "retry the Connection command or deployment that produced this error")
	assert.NotContains(t, localErr.Suggestion, "azd deploy")
	for _, sensitive := range []string{
		endpoint, account, project, "secret-user", "secret-password", "secret-signature", "secret-fragment",
	} {
		assert.NotContains(t, err.Error()+localErr.Suggestion, sensitive)
	}
}

func TestResolveARMContext_DiscoveryFallback(t *testing.T) {
	t.Parallel()

	const projectIDPrefix = "/subscriptions/other-sub/resourceGroups/other-rg/providers/" +
		"Microsoft.CognitiveServices/accounts/"
	for _, tt := range []struct {
		name      string
		projectID string
	}{
		{name: "missing project ID"},
		{name: "malformed project ID", projectID: "not-an-arm-resource-id"},
		{name: "different account", projectID: projectIDPrefix + "other-account/projects/proj-1"},
		{name: "different project", projectID: projectIDPrefix + "cog-xyz/projects/other-project"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lister := &endpointConnectionLister{
				connections: []connections.Connection{{
					ID: "/subscriptions/sub-123/resourceGroups/rg-abc/providers/" +
						"Microsoft.CognitiveServices/accounts/cog-xyz/projects/proj-1/connections/existing",
				}},
			}

			armCtx, err := resolveARMContext(t.Context(), tt.projectID, "cog-xyz", "proj-1", lister)
			require.NoError(t, err)
			assert.Equal(t, 1, lister.calls)
			assert.Equal(t, &armContext{
				SubscriptionID: "sub-123",
				ResourceGroup:  "rg-abc",
				AccountName:    "cog-xyz",
				ProjectName:    "proj-1",
			}, armCtx)
		})
	}
}

type endpointConnectionLister struct {
	connections []connections.Connection
	calls       int
}

func (l *endpointConnectionLister) ListConnections(context.Context) ([]connections.Connection, error) {
	l.calls++
	return l.connections, nil
}
