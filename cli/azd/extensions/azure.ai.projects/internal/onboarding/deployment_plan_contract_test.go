// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const contractDeveloperID = "00000000-0000-0000-0000-000000000044"

func selectedDeploymentContract(t *testing.T, models bool, principalType string) Plan {
	t.Helper()
	plan, err := BuildPlan(contractPlanInput())
	require.NoError(t, err)
	plan.Hosting.Parent = "/subscriptions/foundry-sub/resourceGroups/existing-foundry"
	plan, err = WithDeploymentOperations(plan, DeploymentSelection{
		Models: models, PrincipalID: contractDeveloperID, PrincipalType: principalType,
	})
	require.NoError(t, err)
	return plan
}

func TestSelectedStandardTemplateScopesAndRecipients(t *testing.T) {
	for _, models := range []bool{false, true} {
		plan := selectedDeploymentContract(t, models, "User")
		has := func(action, scope string) bool {
			return slices.ContainsFunc(plan.Operations, func(op Operation) bool { return op.Action == action && op.Scope == scope })
		}
		assert.True(t, has("Microsoft.CognitiveServices/accounts/projects/connections/write", plan.Hosting.Parent))
		assert.Equal(t, models, has("Microsoft.CognitiveServices/accounts/deployments/write", plan.Hosting.Parent))
		for _, action := range []string{"write", "read", "operations/read"} {
			assert.True(t, has("Microsoft.Resources/deployments/"+action, plan.Hosting.Parent))
			assert.False(t, has("Microsoft.Resources/deployments/"+action, plan.Registry.Parent),
				"BYO uses the journaled registry SDK grant, not a nested deployment in the supplied registry RG")
		}
		index := slices.IndexFunc(plan.Operations, func(op Operation) bool { return op.RoleID == cognitiveUserRoleID })
		require.NotEqual(t, -1, index)
		assert.Equal(t, contractDeveloperID, plan.Operations[index].PrincipalID)
		assert.Equal(t, "User", plan.Operations[index].PrincipalType)
		assert.Equal(t, plan.Hosting.Parent, plan.Operations[index].Scope)
	}
}

const cognitiveUserRoleID = "a97b65f3-24c7-4388-baec-2e87135dc908"

func selectedOperationAuthorizer(t *testing.T, scope, excluded, condition string) *scopedAuthorizer {
	t.Helper()
	options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, request.Method, "selected-operation checks must remain read-only")
		var body string
		switch {
		case strings.Contains(request.URL.Path, "/denyAssignments"):
			body = `{"value":[]}`
		case strings.Contains(request.URL.Path, "/roleAssignments"):
			body = fmt.Sprintf(`{"value":[{"properties":{"scope":%q,"principalId":"operator","principalType":"User",
				"roleDefinitionId":"/providers/Microsoft.Authorization/roleDefinitions/custom",
				"condition":%q,"conditionVersion":"2.0"}}]}`, scope, condition)
		case strings.Contains(request.URL.Path, "/roleDefinitions"):
			body = fmt.Sprintf(`{"properties":{"permissions":[{"actions":["*"],"notActions":[%q]}]}}`, excluded)
		default:
			t.Fatalf("unexpected selected-operation read %s", request.URL.Path)
		}
		return contractARMResponse(request, 200, body), nil
	})
	return &scopedAuthorizer{credential: contractCredential{}, options: onboardingARMOptions(options)}
}

func TestDeveloperDelegationUsesActualRoleRecipientAndType(t *testing.T) {
	const attribute = "@Request[Microsoft.Authorization/roleAssignments:"
	condition := attribute + "RoleDefinitionId] GuidEquals {'" + cognitiveUserRoleID + "'} AND " +
		attribute + "PrincipalId] GuidEquals {'" + contractDeveloperID + "'} AND " +
		attribute + "PrincipalType] StringEquals 'User'"
	for _, principalType := range []string{"User", "ServicePrincipal"} {
		t.Run(principalType, func(t *testing.T) {
			plan := selectedDeploymentContract(t, false, principalType)
			index := slices.IndexFunc(plan.Operations, func(op Operation) bool { return op.RoleID == cognitiveUserRoleID })
			require.NotEqual(t, -1, index)
			op := plan.Operations[index]
			auth := selectedOperationAuthorizer(t, op.Scope, "", condition)
			status, reason, err := auth.evaluate(t.Context(), callerIdentity{objectID: "operator"}, op)
			require.NoError(t, err)
			want := Pass
			if principalType != "User" {
				want = Fail
			}
			assert.Equal(t, want, status, reason)
		})
	}
}
