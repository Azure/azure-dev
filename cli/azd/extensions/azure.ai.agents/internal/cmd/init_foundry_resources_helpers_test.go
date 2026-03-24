// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractProjectDetails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		resourceId  string
		wantSub     string
		wantRG      string
		wantAccount string
		wantProject string
		wantErr     bool
	}{
		{
			name: "valid resource ID",
			resourceId: "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/my-rg/" +
				"providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project",
			wantSub:     "00000000-0000-0000-0000-000000000001",
			wantRG:      "my-rg",
			wantAccount: "my-account",
			wantProject: "my-project",
		},
		{
			name: "resource ID with special characters in names",
			resourceId: "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resourceGroups/rg-with-dashes/" +
				"providers/Microsoft.CognitiveServices/accounts/account_underscore/projects/proj.dots",
			wantSub:     "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			wantRG:      "rg-with-dashes",
			wantAccount: "account_underscore",
			wantProject: "proj.dots",
		},
		{
			name:       "empty string",
			resourceId: "",
			wantErr:    true,
		},
		{
			name:       "malformed - missing projects segment",
			resourceId: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.CognitiveServices/accounts/acct1",
			wantErr:    true,
		},
		{
			name:       "malformed - wrong provider",
			resourceId: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Storage/accounts/acct1/projects/proj1",
			wantErr:    true,
		},
		{
			name:       "malformed - extra trailing segment",
			resourceId: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.CognitiveServices/accounts/acct1/projects/proj1/extra",
			wantErr:    true,
		},
		{
			name:       "malformed - random string",
			resourceId: "not-a-resource-id",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := extractProjectDetails(tt.resourceId)

			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.wantSub, result.SubscriptionId)
			require.Equal(t, tt.wantRG, result.ResourceGroupName)
			require.Equal(t, tt.wantAccount, result.AccountName)
			require.Equal(t, tt.wantProject, result.ProjectName)
			require.Equal(t, tt.resourceId, result.ResourceId)
		})
	}
}

func TestNormalizeLoginServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain hostname",
			input: "myregistry.azurecr.io",
			want:  "myregistry.azurecr.io",
		},
		{
			name:  "https prefix",
			input: "https://myregistry.azurecr.io",
			want:  "myregistry.azurecr.io",
		},
		{
			name:  "http prefix",
			input: "http://myregistry.azurecr.io",
			want:  "myregistry.azurecr.io",
		},
		{
			name:  "https with trailing slash",
			input: "https://myregistry.azurecr.io/",
			want:  "myregistry.azurecr.io",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, normalizeLoginServer(tt.input))
		})
	}
}

func TestFoundryProjectInfoResourceIdConstruction(t *testing.T) {
	t.Parallel()

	// Verify round-trip: parse a resource ID then reconstruct it
	originalId := "/subscriptions/aaaa/resourceGroups/rg-test/providers/Microsoft.CognitiveServices/accounts/acct-1/projects/proj-1"

	info, err := extractProjectDetails(originalId)
	require.NoError(t, err)

	reconstructed := "/subscriptions/" + info.SubscriptionId +
		"/resourceGroups/" + info.ResourceGroupName +
		"/providers/Microsoft.CognitiveServices/accounts/" + info.AccountName +
		"/projects/" + info.ProjectName

	require.Equal(t, originalId, reconstructed)
}
