// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func selectedDeploymentContract(t *testing.T) Plan {
	t.Helper()
	plan, err := BuildPlan(contractPlanInput())
	require.NoError(t, err)
	plan, err = WithDeploymentOperations(plan, DeploymentSelection{})
	require.NoError(t, err)
	return plan
}

func TestSelectedBYOCrossGroupDeploymentScopes(t *testing.T) {
	plan := selectedDeploymentContract(t)
	var want []Operation
	for _, scope := range []string{plan.Cluster.Parent, plan.Storage.Parent, plan.Registry.Parent} {
		for _, action := range []string{"write", "read", "operations/read"} {
			want = append(want, Operation{
				CheckID: "P09", Scope: scope, Action: "Microsoft.Resources/deployments/" + action,
			})
		}
	}
	got := slices.DeleteFunc(slices.Clone(plan.Operations), func(op Operation) bool { return op.CheckID != "P09" })
	assert.ElementsMatch(t, want, got)
	assert.False(t, slices.ContainsFunc(plan.Operations, func(op Operation) bool {
		return op.Action == roleAssignmentWrite
	}), "scoped grant operations are derived from the plan by the authorizer")
}

func selectedOperationAuthorizer(t *testing.T, scope, excluded string) *scopedAuthorizer {
	t.Helper()
	options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, request.Method, "selected-operation checks must remain read-only")
		var body string
		switch {
		case strings.Contains(request.URL.Path, "/roleAssignments"):
			body = fmt.Sprintf(`{"value":[{"properties":{"scope":%q,"principalId":"operator","principalType":"User",
				"roleDefinitionId":"/providers/Microsoft.Authorization/roleDefinitions/custom"}}]}`, scope)
		case strings.Contains(request.URL.Path, "/roleDefinitions"):
			body = fmt.Sprintf(`{"properties":{"permissions":[{"actions":["*"],"notActions":[%q]}]}}`, excluded)
		default:
			t.Fatalf("unexpected selected-operation read %s", request.URL.Path)
		}
		return contractARMResponse(request, 200, body), nil
	})
	return &scopedAuthorizer{credential: contractCredential{}, options: onboardingARMOptions(options)}
}

func TestMissingSelectedBYOCrossGroupActionBlocksPreflight(t *testing.T) {
	plan := selectedDeploymentContract(t)
	for _, op := range plan.Operations {
		if op.CheckID != "P09" {
			continue
		}
		t.Run(op.Action+"/"+op.Scope, func(t *testing.T) {
			auth := selectedOperationAuthorizer(t, op.Scope, op.Action)
			status, reason, err := auth.basicAction(t.Context(), op.Scope, callerIdentity{objectID: "operator"}, op.Action, false)
			require.NoError(t, err)
			require.Equal(t, Fail, status, reason)
			pipeline := Pipeline{
				Resources: contractResources(func(context.Context, Plan) (Facts, Report, error) {
					return Facts{}, nil, nil
				}),
				Access: contractAccess(func(context.Context, Plan, Facts) (Report, error) {
					return Report{{ID: "P09", Scope: op.Scope, Status: status, Mandatory: true,
						Expected: op.Action, Observed: reason}}, nil
				}),
			}
			_, _, err = pipeline.Preflight(t.Context(), plan)
			require.Error(t, err)
		})
	}
}
