// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindReservedResourceNameViolation(t *testing.T) {
	tests := []struct {
		name         string
		resourceName string
		wantSegment  string
		wantWord     string
		wantMatch    string
		wantFound    bool
	}{
		{
			name:         "valid resource name",
			resourceName: "my-resource-name",
		},
		{
			name:         "exact match reserved word",
			resourceName: "azure",
			wantSegment:  "azure",
			wantWord:     "AZURE",
			wantMatch:    "exactly matches",
			wantFound:    true,
		},
		{
			name:         "substring reserved word",
			resourceName: "project-MicrosoftLearnAgent",
			wantSegment:  "project-MicrosoftLearnAgent",
			wantWord:     "MICROSOFT",
			wantMatch:    "contains",
			wantFound:    true,
		},
		{
			name:         "prefix reserved word",
			resourceName: "LoginPortal",
			wantSegment:  "LoginPortal",
			wantWord:     "LOGIN",
			wantMatch:    "starts with",
			wantFound:    true,
		},
		{
			name:         "checks individual resource name segments",
			resourceName: "ai-account/AZURE",
			wantSegment:  "AZURE",
			wantWord:     "AZURE",
			wantMatch:    "exactly matches",
			wantFound:    true,
		},
		{
			name:         "checks child resource segment",
			resourceName: "ai-account/project-MicrosoftLearnAgent",
			wantSegment:  "project-MicrosoftLearnAgent",
			wantWord:     "MICROSOFT",
			wantMatch:    "contains",
			wantFound:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSegment, gotWord, gotMatch, gotFound := findReservedResourceNameViolation(tt.resourceName)
			require.Equal(t, tt.wantFound, gotFound)
			require.Equal(t, tt.wantSegment, gotSegment)
			require.Equal(t, tt.wantWord, gotWord)
			require.Equal(t, tt.wantMatch, gotMatch)
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
				Type: "Microsoft.Storage/storageAccounts",
				Name: "validname",
			},
		},
	})

	require.NoError(t, err)
	require.Len(t, results, 2)

	require.Equal(t, PreflightCheckWarning, results[0].Severity)
	require.Equal(t, "reserved_resource_name", results[0].DiagnosticID)
	require.Contains(t, results[0].Message, `"ai-account/project-MicrosoftLearnAgent"`)
	require.Contains(t, results[0].Message, `"MICROSOFT"`)

	require.Equal(t, PreflightCheckWarning, results[1].Severity)
	require.Equal(t, "reserved_resource_name", results[1].DiagnosticID)
	require.Contains(t, results[1].Message, `"LoginPortal"`)
	require.Contains(t, results[1].Message, `"LOGIN"`)
}
