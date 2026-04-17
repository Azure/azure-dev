// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindReservedResourceNameViolations(t *testing.T) {
	tests := []struct {
		name         string
		resourceName string
		want         []reservedNameViolation
	}{
		{
			name:         "valid resource name",
			resourceName: "my-resource-name",
		},
		{
			name:         "empty resource name",
			resourceName: "",
		},
		{
			name:         "empty segments are skipped",
			resourceName: "/",
		},
		{
			name:         "exact match reserved word",
			resourceName: "azure",
			want: []reservedNameViolation{
				{segment: "azure", reservedWord: "AZURE", matchType: "exactly matches"},
			},
		},
		{
			name:         "substring reserved word",
			resourceName: "project-MicrosoftLearnAgent",
			want: []reservedNameViolation{
				{segment: "project-MicrosoftLearnAgent", reservedWord: "MICROSOFT", matchType: "contains"},
			},
		},
		{
			name:         "prefix reserved word",
			resourceName: "LoginPortal",
			want: []reservedNameViolation{
				{segment: "LoginPortal", reservedWord: "LOGIN", matchType: "starts with"},
			},
		},
		{
			name:         "checks individual resource name segments",
			resourceName: "ai-account/AZURE",
			want: []reservedNameViolation{
				{segment: "AZURE", reservedWord: "AZURE", matchType: "exactly matches"},
			},
		},
		{
			name:         "checks child resource segment",
			resourceName: "ai-account/project-MicrosoftLearnAgent",
			want: []reservedNameViolation{
				{segment: "project-MicrosoftLearnAgent", reservedWord: "MICROSOFT", matchType: "contains"},
			},
		},
		{
			name:         "reports multiple violations in single segment",
			resourceName: "LoginMicrosoftApp",
			want: []reservedNameViolation{
				{segment: "LoginMicrosoftApp", reservedWord: "LOGIN", matchType: "starts with"},
				{segment: "LoginMicrosoftApp", reservedWord: "MICROSOFT", matchType: "contains"},
			},
		},
		{
			name:         "reports violations across multiple segments",
			resourceName: "Azure/LoginPortal",
			want: []reservedNameViolation{
				{segment: "Azure", reservedWord: "AZURE", matchType: "exactly matches"},
				{segment: "LoginPortal", reservedWord: "LOGIN", matchType: "starts with"},
			},
		},
		{
			name: "skips unresolved ARM expression containing provider namespaces",
			resourceName: "[guid('/subscriptions/sub-id/resourceGroups/" +
				"rg-learn-agent-dev/providers/Microsoft.ContainerRegistry/" +
				"registries/cr123')]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findReservedResourceNameViolations(tt.resourceName)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCheckReservedResourceNames(t *testing.T) {
	provider := &BicepProvider{}

	results, err := provider.checkReservedResourceNames(t.Context(), &validationContext{
		SnapshotResources: []armTemplateResource{
			{
				Type: "Microsoft.CognitiveServices/accounts/projects",
				Name: "ai-account/project-MicrosoftLearnAgent",
			},
			{
				Type: "Microsoft.Web/sites",
				Name: "LoginPortal",
			},
			{
				// Triggers both LOGIN prefix and MICROSOFT substring rules in
				// a single segment — both should be reported as separate results.
				Type: "Microsoft.Web/sites",
				Name: "LoginMicrosoftApp",
			},
			{
				Type: "Microsoft.Storage/storageAccounts",
				Name: "validname",
			},
			{
				// Unresolved ARM expression — should be skipped even though it
				// contains provider namespaces like "Microsoft.ContainerRegistry".
				Type: "Microsoft.Authorization/roleAssignments",
				Name: "[guid('/subscriptions/sub-id/resourceGroups/rg-learn-agent-dev/providers/" +
					"Microsoft.ContainerRegistry/registries/cr123', principalId, roleDefId)]",
			},
		},
	})

	require.NoError(t, err)
	require.Len(t, results, 4)

	for _, r := range results {
		require.Equal(t, PreflightCheckWarning, r.Severity)
		require.Equal(t, "reserved_resource_name", r.DiagnosticID)
	}

	// Child resource violation.
	require.Contains(t, results[0].Message, `"ai-account/project-MicrosoftLearnAgent"`)
	require.Contains(t, results[0].Message, "contains")
	require.Contains(t, results[0].Message, `"MICROSOFT"`)

	// Top-level resource violation.
	require.Contains(t, results[1].Message, `"LoginPortal"`)
	require.Contains(t, results[1].Message, "starts with")
	require.Contains(t, results[1].Message, `"LOGIN"`)

	// Both violations on LoginMicrosoftApp should be reported as distinct results.
	require.Contains(t, results[2].Message, `"LoginMicrosoftApp"`)
	require.Contains(t, results[2].Message, "starts with")
	require.Contains(t, results[2].Message, `"LOGIN"`)
	require.Contains(t, results[3].Message, `"LoginMicrosoftApp"`)
	require.Contains(t, results[3].Message, "contains")
	require.Contains(t, results[3].Message, `"MICROSOFT"`)
}
