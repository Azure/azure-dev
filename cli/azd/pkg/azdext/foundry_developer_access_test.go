// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/stretchr/testify/require"
)

type fakeRoleAssignments struct {
	hasRole    bool
	hasErr     error
	assignErr  error
	assignCall int
	roleID     string
}

func (f *fakeRoleAssignments) hasAnyRole(context.Context, string, []string, string) (bool, error) {
	return f.hasRole, f.hasErr
}

func (f *fakeRoleAssignments) assignRole(
	_ context.Context, _, roleID, _ string, _ armauthorization.PrincipalType,
) error {
	f.assignCall++
	f.roleID = roleID
	return f.assignErr
}

func testScope(t *testing.T) *foundryProjectScope {
	t.Helper()
	scope, err := parseFoundryProjectScope(
		"/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj")
	require.NoError(t, err)
	return scope
}

func TestParseFoundryProjectScope(t *testing.T) {
	scope := testScope(t)
	require.Equal(t, "sub1", scope.subscriptionID)
	require.Equal(t, "acct", scope.accountName)
	require.Equal(t, "proj", scope.projectName)

	_, err := parseFoundryProjectScope("/subscriptions/sub1/resourceGroups/rg1")
	require.Error(t, err)
}

func TestEnsureFoundryUser(t *testing.T) {
	forbidden := &azcore.ResponseError{StatusCode: http.StatusForbidden}

	tests := []struct {
		name       string
		api        *fakeRoleAssignments
		wantAssign int
		wantOutput string
	}{
		{
			name:       "role already present",
			api:        &fakeRoleAssignments{hasRole: true},
			wantOutput: "✓ Foundry User on Foundry Project",
		},
		{
			name:       "role missing and assigned",
			api:        &fakeRoleAssignments{},
			wantAssign: 1,
			wantOutput: "auto-assigned",
		},
		{
			name:       "assignment denied warns only",
			api:        &fakeRoleAssignments{assignErr: forbidden},
			wantAssign: 1,
			wantOutput: "auto-assign was denied",
		},
		{
			name:       "non-auth assignment failure continues",
			api:        &fakeRoleAssignments{assignErr: errors.New("boom")},
			wantAssign: 1,
			wantOutput: "non-auth error",
		},
		{
			name:       "role lookup failure does not assign",
			api:        &fakeRoleAssignments{hasErr: errors.New("list failed")},
			wantOutput: "Could not check Foundry User role",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			ensureFoundryUser(
				t.Context(), tt.api, &out, testScope(t), "oid", armauthorization.PrincipalTypeUser)
			require.Equal(t, tt.wantAssign, tt.api.assignCall)
			require.Contains(t, out.String(), tt.wantOutput)
			if tt.wantAssign > 0 {
				require.Equal(t, RoleFoundryUser, tt.api.roleID)
			}
		})
	}
}
