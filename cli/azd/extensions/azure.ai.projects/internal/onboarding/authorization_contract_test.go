// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDelegationConditions(t *testing.T) {
	const role = "7f951dda-4ed3-4680-a7ca-43fe172d538d"
	const principal = "00000000-0000-0000-0000-000000000123"
	const request = "@Request[Microsoft.Authorization/roleAssignments:"
	op := Operation{Action: roleAssignmentWrite, RoleID: "/providers/Microsoft.Authorization/roleDefinitions/" + role,
		PrincipalID: principal}
	for _, tc := range []struct {
		name, condition string
		status          Status
	}{
		{"unconditional", "", Pass},
		{"role", request + "RoleDefinitionId] ForAnyOfAnyValues:GuidEquals {'" + role + "'}", Pass},
		{"wrong role", request + "RoleDefinitionId] GuidEquals {'00000000-0000-0000-0000-000000000124'}", Fail},
		{"type", request + "PrincipalType] StringEqualsIgnoreCase 'serviceprincipal'", Pass},
		{"known kubelet", request + "PrincipalId] GuidEquals {'" + principal + "'}", Pass},
		{"unknown attribute", "@Resource[unavailable] StringEquals 'x'", Unverified},
		{"unsupported operator", request + "PrincipalType] StringLike 'Service*'", Unverified},
		{"malformed", "(ActionMatches{'x'}", Unverified},
		{"injection", request + "PrincipalType] StringEquals 'ServicePrincipal' trailing", Unverified},
		{"standard delegation", "((!(ActionMatches{'Microsoft.Authorization/roleAssignments/write'})) OR (" +
			request + "RoleDefinitionId] ForAnyOfAnyValues:GuidEquals {'" + role + "'} AND " +
			request + "PrincipalType] StringEqualsIgnoreCase 'ServicePrincipal'))", Pass},
		{"and rejects", request + "PrincipalType] StringEquals 'User' AND " +
			request + "RoleDefinitionId] GuidEquals {'" + role + "'}", Fail},
		{"unknown or permitted", "@Resource[unknown] GuidEquals {'x'} OR " +
			request + "PrincipalType] StringEquals 'ServicePrincipal'", Pass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := delegationCondition(tc.condition, op)
			assert.Equal(t, tc.status, status)
		})
	}
	op.PrincipalID = ""
	status, reason := delegationCondition(request+"PrincipalId] GuidEquals {'"+principal+"'}", op)
	assert.Equal(t, Unverified, status)
	assert.Contains(t, reason, "unavailable")
}

func TestScopedPermissionAndDenyRules(t *testing.T) {
	permissions := []*armauthorization.Permission{
		{Actions: []*string{new("*")}, NotActions: []*string{new(roleAssignmentWrite)}},
		{Actions: []*string{new(roleAssignmentWrite)}},
	}
	assert.True(t, permissionAllows(permissions, roleAssignmentWrite), "NotActions is per granting permission")
	assert.False(t, permissionAllows(permissions[:1], roleAssignmentWrite))
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"oid":"operator","groups":["group"]}`))
	caller, err := identityFromToken("header." + claims + ".signature")
	require.NoError(t, err)
	require.True(t, caller.includes("GROUP"))
	require.False(t, scopeContains("/subscriptions/a/resourceGroups/rg", "/subscriptions/a/resourceGroups/rg-other"))
	for _, tc := range []struct {
		name       string
		exclude    string
		doNotApply bool
		condition  string
		status     Status
	}{
		{"deny inherited group", "", false, "", Fail},
		{"explicit exclusion", "operator", false, "", Pass},
		{"no child application", "", true, "", Pass},
		{"conditional deny unknown", "", false, "unsupported", Unverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			properties := &armauthorization.DenyAssignmentProperties{
				Scope: new("/subscriptions/a"), DoNotApplyToChildScopes: new(tc.doNotApply),
				Principals: []*armauthorization.Principal{{ID: new("group"), Type: new("Group")}},
				Permissions: []*armauthorization.DenyAssignmentPermission{{
					Actions: []*string{new("*")}, Condition: new(tc.condition),
				}},
			}
			if tc.exclude != "" {
				properties.ExcludePrincipals = []*armauthorization.Principal{{ID: new(tc.exclude), Type: new("User")}}
			}
			status, _ := evaluateDeny(caller, Operation{Action: roleAssignmentWrite,
				Scope: "/subscriptions/a/resourceGroups/rg"}, []*armauthorization.DenyAssignment{{Properties: properties}})
			assert.Equal(t, tc.status, status)
		})
	}
}

func TestExistingParentDiscoveryUsesScopedPermissions(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			input := contractPlanInput()
			input.StorageID, input.RegistryID = "", ""
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				assert.Equal(t, http.MethodGet, request.Method)
				if !exists {
					return contractARMResponse(request, 404, `{"error":{"code":"ResourceGroupNotFound"}}`), nil
				}
				return contractARMResponse(request, 200, `{"id":"`+request.URL.Path+`","location":"eastus"}`), nil
			})
			refined, err := ResolveExistingParents(t.Context(), plan, contractCredential{}, options)
			require.NoError(t, err)
			want := "/subscriptions/" + plan.SubscriptionID
			if exists {
				want += "/resourceGroups/" + plan.ResourceGroup
			}
			assert.Equal(t, want, refined.Hosting.Parent)
			assert.Equal(t, want, refined.Workload.Parent)
			assert.Equal(t, want, refined.Storage.Parent)
			assert.Equal(t, want, refined.Registry.Parent)
			for _, op := range refined.Operations {
				if op.CheckID == "P01" || strings.Contains(op.Action, "/deployments/") {
					assert.Equal(t, "/subscriptions/"+plan.SubscriptionID, op.Scope)
				}
				if op.CheckID == "P02" || op.CheckID == "P04" || op.CheckID == "P05" {
					assert.Equal(t, want, op.Scope)
				}
			}
			assert.NotEqual(t, "", plan.Hosting.Parent)
		})
	}
}

func TestScopedAuthorizerReadsOnlyAndEvaluatesKnownRecipient(t *testing.T) {
	scope := "/subscriptions/sub/resourceGroups/rg"
	condition := "@Request[Microsoft.Authorization/roleAssignments:PrincipalType] StringEquals 'ServicePrincipal'"
	options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, request.Method)
		var body string
		switch {
		case strings.Contains(request.URL.Path, "/denyAssignments"):
			body = `{"value":[]}`
		case strings.Contains(request.URL.Path, "/roleAssignments"):
			body = fmt.Sprintf(`{"value":[{"properties":{"scope":%q,"principalId":"operator","principalType":"User",
				"roleDefinitionId":"/providers/Microsoft.Authorization/roleDefinitions/custom",
				"condition":%q,"conditionVersion":"2.0"}}]}`, scope, condition)
		case strings.Contains(request.URL.Path, "/roleDefinitions"):
			body = `{"properties":{"permissions":[{"actions":["Microsoft.Authorization/roleAssignments/write"]}]}}`
		default:
			t.Fatalf("unexpected read: %s", request.URL.Path)
		}

		return contractARMResponse(request, 200, body), nil
	})
	auth := &scopedAuthorizer{credential: contractCredential{}, options: options}
	status, reason, err := auth.evaluate(t.Context(), callerIdentity{objectID: "operator"},
		Operation{Action: roleAssignmentWrite, Scope: scope, RoleID: registryRoleID(true)})
	require.NoError(t, err)
	assert.Equal(t, Pass, status, reason)
}

func TestScopedAuthorizerConditionalGrantOutcomes(t *testing.T) {
	const scope = "/subscriptions/sub/resourceGroups/rg"
	const attribute = "@Request[Microsoft.Authorization/roleAssignments:"
	for _, tc := range []struct {
		name, condition, principal string
		want                       Status
	}{
		{"known true", attribute + "PrincipalType] StringEquals 'ServicePrincipal'", "", Pass},
		{"known false", attribute + "PrincipalType] StringEquals 'User'", "", Fail},
		{"unsupported evidence", "@Resource[unknown] GuidEquals {'x'}", "", Unverified},
		{"future identity", attribute + "PrincipalId] GuidEquals {'kubelet'}", "", Unverified},
		{"known recipient", attribute + "PrincipalId] GuidEquals {'kubelet'}", "kubelet", Pass},
		{"different recipient", attribute + "PrincipalId] GuidEquals {'kubelet'}", "different", Fail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				var body string
				switch {
				case strings.Contains(request.URL.Path, "/denyAssignments"):
					body = `{"value":[]}`
				case strings.Contains(request.URL.Path, "/roleAssignments"):
					body = fmt.Sprintf(`{"value":[{"properties":{
						"scope":%q,"principalId":"operator","principalType":"User",
						"roleDefinitionId":"/providers/Microsoft.Authorization/roleDefinitions/custom",
						"condition":%q,"conditionVersion":"2.0"}}]}`, scope, tc.condition)
				case strings.Contains(request.URL.Path, "/roleDefinitions"):
					body = `{"properties":{"permissions":[{"actions":["Microsoft.Authorization/roleAssignments/write"]}]}}`
				default:
					t.Fatalf("unexpected authorization request: %s", request.URL.Path)
				}
				return contractARMResponse(request, 200, body), nil
			})
			auth := &scopedAuthorizer{credential: contractCredential{}, options: onboardingARMOptions(options)}
			status, reason, err := auth.evaluate(t.Context(), callerIdentity{objectID: "operator"},
				Operation{Action: roleAssignmentWrite, Scope: scope, RoleID: registryRoleID(true),
					PrincipalID: tc.principal})
			require.NoError(t, err)
			assert.Equal(t, tc.want, status, reason)
		})
	}
}
