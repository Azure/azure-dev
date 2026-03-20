// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/stretchr/testify/require"
)

// roleDefinitionProperties describes a role for use in test helpers.
type roleDefinitionProperties struct {
	actions    []string
	notActions []string
}

// fakeRoleDefTransport is a minimal HTTP transport that returns canned role definition
// responses keyed by role definition ID (matched as a URL path suffix).
type fakeRoleDefTransport struct {
	definitions map[string]*roleDefinitionProperties
}

func (f *fakeRoleDefTransport) Do(req *http.Request) (*http.Response, error) {
	for id, def := range f.definitions {
		// Match by checking that the URL path ends with the role definition ID.
		if strings.HasSuffix(req.URL.Path, "/"+id) || req.URL.Path == "/"+id {
			actions := make([]*string, len(def.actions))
			for i := range def.actions {
				actions[i] = &def.actions[i]
			}
			notActions := make([]*string, len(def.notActions))
			for i := range def.notActions {
				notActions[i] = &def.notActions[i]
			}

			resp := armauthorization.RoleDefinitionsClientGetByIDResponse{
				RoleDefinition: armauthorization.RoleDefinition{
					Properties: &armauthorization.RoleDefinitionProperties{
						Permissions: []*armauthorization.Permission{{
							Actions:    actions,
							NotActions: notActions,
						}},
					},
				},
			}

			body, _ := json.Marshal(resp.RoleDefinition)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Request:    req,
				Body:       io.NopCloser(bytes.NewBuffer(body)),
			}, nil
		}
	}
	return nil, fmt.Errorf("no mock for role definition request: %s", req.URL.Path)
}

// newFakeRoleDefinitionsClient creates a *armauthorization.RoleDefinitionsClient backed
// by a fake HTTP transport that returns canned role definitions.
func newFakeRoleDefinitionsClient(
	t *testing.T, definitions map[string]*roleDefinitionProperties,
) *armauthorization.RoleDefinitionsClient {
	t.Helper()

	transport := &fakeRoleDefTransport{definitions: definitions}
	client, err := armauthorization.NewRoleDefinitionsClient(
		&fakeCredential{},
		&arm.ClientOptions{
			ClientOptions: azcore.ClientOptions{
				Transport: transport,
			},
		},
	)
	require.NoError(t, err)
	return client
}

// fakeCredential satisfies azcore.TokenCredential for test clients.
type fakeCredential struct{}

func (f *fakeCredential) GetToken(
	_ context.Context, _ policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token"}, nil
}

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

func TestCheckActionsFromRoles_ConditionalThenUnconditional(t *testing.T) {
	// Verifies that when a conditional role grants an action first, a later
	// unconditional role still clears the Conditional flag. This exercises the
	// per-action unconditional tracking (unconditionalActions map) to ensure
	// ordering doesn't produce false positive conditional warnings.
	service := &PermissionsService{}

	// Two assignments: first is conditional, second is unconditional.
	// Both grant the same action via wildcard.
	assignments := []roleAssignmentInfo{
		{roleDefinitionID: "conditional-role", hasCondition: true},
		{roleDefinitionID: "unconditional-role", hasCondition: false},
	}

	// Mock role definitions client via a fakeRoleDefinitionsClient.
	definitions := map[string]*roleDefinitionProperties{
		"conditional-role": {
			actions:    []string{"Microsoft.Authorization/*"},
			notActions: nil,
		},
		"unconditional-role": {
			actions:    []string{"*"},
			notActions: nil,
		},
	}

	client := newFakeRoleDefinitionsClient(t, definitions)
	result, err := service.checkActionsFromRoles(
		t.Context(), client,
		assignments,
		[]string{"Microsoft.Authorization/roleAssignments/write"},
	)
	require.NoError(t, err)
	require.True(t, result.HasPermission, "action should be granted")
	require.False(t, result.Conditional,
		"unconditional role should clear the conditional flag even when seen after a conditional role")
}

func TestCheckActionsFromRoles_AllConditional(t *testing.T) {
	// When all granting roles have ABAC conditions, Conditional should be true.
	service := &PermissionsService{}

	assignments := []roleAssignmentInfo{
		{roleDefinitionID: "cond-role-1", hasCondition: true},
		{roleDefinitionID: "cond-role-2", hasCondition: true},
	}

	definitions := map[string]*roleDefinitionProperties{
		"cond-role-1": {
			actions: []string{"Microsoft.Authorization/roleAssignments/*"},
		},
		"cond-role-2": {
			actions: []string{"*"},
		},
	}

	client := newFakeRoleDefinitionsClient(t, definitions)
	result, err := service.checkActionsFromRoles(
		t.Context(), client,
		assignments,
		[]string{"Microsoft.Authorization/roleAssignments/write"},
	)
	require.NoError(t, err)
	require.True(t, result.HasPermission)
	require.True(t, result.Conditional,
		"all granting roles are conditional, so result should be conditional")
}

func TestCheckActionsFromRoles_MultipleActions_MixedConditionality(t *testing.T) {
	// Two required actions: action A granted only by a conditional role,
	// action B granted by an unconditional role. Result should be Conditional
	// because action A has no unconditional grant.
	service := &PermissionsService{}

	assignments := []roleAssignmentInfo{
		{roleDefinitionID: "cond-role", hasCondition: true},
		{roleDefinitionID: "uncond-role", hasCondition: false},
	}

	definitions := map[string]*roleDefinitionProperties{
		"cond-role": {
			actions: []string{"Microsoft.Authorization/roleAssignments/*"},
		},
		"uncond-role": {
			actions: []string{"Microsoft.Compute/*"},
		},
	}

	client := newFakeRoleDefinitionsClient(t, definitions)
	result, err := service.checkActionsFromRoles(
		t.Context(), client,
		assignments,
		[]string{
			"Microsoft.Authorization/roleAssignments/write",
			"Microsoft.Compute/virtualMachines/write",
		},
	)
	require.NoError(t, err)
	require.True(t, result.HasPermission)
	require.True(t, result.Conditional,
		"action A only has conditional grants, so overall result should be conditional")
}
