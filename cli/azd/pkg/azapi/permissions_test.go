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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := actionMatches(tt.pattern, tt.target)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIsActionAllowed(t *testing.T) {
	tests := []struct {
		name           string
		requiredAction string
		actions        *allowedActionSet
		want           bool
	}{
		{
			name:           "allowed by exact match",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions: &allowedActionSet{
				actions:    []string{"Microsoft.Authorization/roleAssignments/write"},
				notActions: nil,
			},
			want: true,
		},
		{
			name:           "allowed by wildcard",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions: &allowedActionSet{
				actions:    []string{"*"},
				notActions: nil,
			},
			want: true,
		},
		{
			name:           "denied by NotActions",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions: &allowedActionSet{
				actions:    []string{"*"},
				notActions: []string{"Microsoft.Authorization/roleAssignments/write"},
			},
			want: false,
		},
		{
			name:           "denied by NotActions wildcard",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions: &allowedActionSet{
				actions:    []string{"*"},
				notActions: []string{"Microsoft.Authorization/*"},
			},
			want: false,
		},
		{
			name:           "not matched at all",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions: &allowedActionSet{
				actions:    []string{"Microsoft.Storage/*"},
				notActions: nil,
			},
			want: false,
		},
		{
			name:           "empty actions",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions: &allowedActionSet{
				actions:    nil,
				notActions: nil,
			},
			want: false,
		},
		{
			name:           "allowed by provider wildcard not blocked",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions: &allowedActionSet{
				actions:    []string{"Microsoft.Authorization/*"},
				notActions: []string{"Microsoft.Storage/*"},
			},
			want: true,
		},
		{
			name:           "Contributor role pattern - allowed by star but blocked by NotActions",
			requiredAction: "Microsoft.Authorization/roleAssignments/write",
			actions: &allowedActionSet{
				actions: []string{"*"},
				notActions: []string{
					"Microsoft.Authorization/*/Delete",
					"Microsoft.Authorization/*/Write",
					"Microsoft.Authorization/elevateAccess/Action",
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isActionAllowed(tt.requiredAction, tt.actions)
			require.Equal(t, tt.want, got)
		})
	}
}
