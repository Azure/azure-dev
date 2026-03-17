// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActionMatches(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		target  string
		want    bool
	}{
		{
			name:    "exact match",
			pattern: "Microsoft.Authorization/roleAssignments/write",
			target:  "Microsoft.Authorization/roleAssignments/write",
			want:    true,
		},
		{
			name:    "case insensitive match",
			pattern: "microsoft.authorization/roleassignments/write",
			target:  "Microsoft.Authorization/roleAssignments/write",
			want:    true,
		},
		{
			name:    "full wildcard",
			pattern: "*",
			target:  "Microsoft.Authorization/roleAssignments/write",
			want:    true,
		},
		{
			name:    "trailing wildcard match",
			pattern: "Microsoft.Authorization/*",
			target:  "Microsoft.Authorization/roleAssignments/write",
			want:    true,
		},
		{
			name:    "trailing wildcard at resource level",
			pattern: "Microsoft.Authorization/roleAssignments/*",
			target:  "Microsoft.Authorization/roleAssignments/write",
			want:    true,
		},
		{
			name:    "no match different provider",
			pattern: "Microsoft.Storage/*",
			target:  "Microsoft.Authorization/roleAssignments/write",
			want:    false,
		},
		{
			name:    "no match different action",
			pattern: "Microsoft.Authorization/roleAssignments/read",
			target:  "Microsoft.Authorization/roleAssignments/write",
			want:    false,
		},
		{
			name:    "empty pattern",
			pattern: "",
			target:  "Microsoft.Authorization/roleAssignments/write",
			want:    false,
		},
		{
			name:    "multi-wildcard match any provider",
			pattern: "*/roleAssignments/write",
			target:  "Microsoft.Authorization/roleAssignments/write",
			want:    true,
		},
		{
			name:    "multi-wildcard match any resource type",
			pattern: "Microsoft.*/roleAssignments/*",
			target:  "Microsoft.Authorization/roleAssignments/write",
			want:    true,
		},
		{
			name:    "multi-wildcard match all segments",
			pattern: "*/*/*",
			target:  "Microsoft.Authorization/roleAssignments/write",
			want:    true,
		},
		{
			name:    "multi-wildcard no match different provider prefix",
			pattern: "Microsoft.*/roleAssignments/*",
			target:  "NotMicrosoft.Authorization/roleAssignments/write",
			want:    false,
		},
		{
			name:    "multi-wildcard no match missing segment",
			pattern: "Microsoft.*/roleAssignments/*",
			target:  "Microsoft.Authorization/write",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := actionMatches(tt.pattern, tt.target)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIsActionAllowedByRole(t *testing.T) {
	tests := []struct {
		name           string
		requiredAction string
		actions        []string
		notActions     []string
		want           bool
	}{
		{
			name:           "allowed by exact match",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions:        []string{"Microsoft.Authorization/roleAssignments/write"},
			want:           true,
		},
		{
			name:           "allowed by wildcard",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions:        []string{"*"},
			want:           true,
		},
		{
			name:           "denied by NotActions",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions:        []string{"*"},
			notActions:     []string{"Microsoft.Authorization/roleAssignments/write"},
			want:           false,
		},
		{
			name:           "denied by NotActions wildcard",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions:        []string{"*"},
			notActions:     []string{"Microsoft.Authorization/*"},
			want:           false,
		},
		{
			name:           "not matched at all",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions:        []string{"Microsoft.Storage/*"},
			want:           false,
		},
		{
			name:           "empty actions",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			want:           false,
		},
		{
			name:           "allowed by provider wildcard not blocked",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions:        []string{"Microsoft.Authorization/*"},
			notActions:     []string{"Microsoft.Storage/*"},
			want:           true,
		},
		{
			name:           "Contributor role - allowed by star but blocked by NotActions",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions:        []string{"*"},
			notActions: []string{
				"Microsoft.Authorization/*/Delete",
				"Microsoft.Authorization/*/Write",
				"Microsoft.Authorization/elevateAccess/Action",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isActionAllowedByRole(tt.requiredAction, tt.actions, tt.notActions)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIsActionAllowedByRole_MultiRoleUnion(t *testing.T) {
	// Simulates: Contributor (Actions=*, NotActions=Microsoft.Authorization/*/Write)
	// + User Access Administrator (Actions=Microsoft.Authorization/roleAssignments/*, NotActions=none).
	// Contributor alone denies roleAssignments/write, but User Access Admin grants it.
	// The per-role evaluation should find it allowed via User Access Admin.
	required := "Microsoft.Authorization/roleAssignments/write"

	contributorActions := []string{"*"}
	contributorNotActions := []string{
		"Microsoft.Authorization/*/Delete",
		"Microsoft.Authorization/*/Write",
		"Microsoft.Authorization/elevateAccess/Action",
	}

	uaaActions := []string{
		"Microsoft.Authorization/roleAssignments/*",
		"Microsoft.Authorization/roleAssignments/read",
		"Microsoft.Support/*",
		"*/read",
	}
	var uaaNotActions []string

	// Contributor alone denies it.
	require.False(t, isActionAllowedByRole(required, contributorActions, contributorNotActions))

	// User Access Administrator alone allows it.
	require.True(t, isActionAllowedByRole(required, uaaActions, uaaNotActions))
}
