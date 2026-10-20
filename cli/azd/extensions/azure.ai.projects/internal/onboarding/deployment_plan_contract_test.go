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

func selectedDeploymentContract(t *testing.T, models bool) Plan {
	t.Helper()
	plan, err := BuildPlan(contractPlanInput())
	require.NoError(t, err)
	plan.Hosting.Parent = "/subscriptions/foundry-sub/resourceGroups/existing-foundry"
	plan, err = WithDeploymentOperations(plan, DeploymentSelection{
		Models: models,
	})
	require.NoError(t, err)
	return plan
}

func TestSelectedStandardTemplateScopesAndRecipients(t *testing.T) {
	for _, models := range []bool{false, true} {
		plan := selectedDeploymentContract(t, models)
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
		assert.False(t, slices.ContainsFunc(plan.Operations, func(op Operation) bool {
			return op.Action == roleAssignmentWrite
		}), "developer grants belong to agent predeploy, not the standard provisioning template")
	}
}

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

func TestMissingSelectedStandardTemplateActionStopsPreparation(t *testing.T) {
	plan := selectedDeploymentContract(t, true)
	for _, selected := range []struct{ action, scope string }{
		{"Microsoft.CognitiveServices/accounts/projects/connections/write", plan.Hosting.Parent},
		{"Microsoft.CognitiveServices/accounts/deployments/write", plan.Hosting.Parent},
		{"Microsoft.Resources/deployments/write", plan.Hosting.Parent},
	} {
		t.Run(selected.action+"/"+selected.scope, func(t *testing.T) {
			index := slices.IndexFunc(plan.Operations, func(op Operation) bool {
				return op.Action == selected.action && op.Scope == selected.scope
			})
			require.NotEqual(t, -1, index)
			op := plan.Operations[index]
			auth := selectedOperationAuthorizer(t, op.Scope, op.Action, "")
			status, reason, err := auth.evaluate(t.Context(), callerIdentity{objectID: "operator"}, op)
			require.NoError(t, err)
			require.Equal(t, Fail, status, reason)
			pipeline := Pipeline{
				Resources: contractResources(func(context.Context, Plan) (Facts, Report, error) { return Facts{}, nil, nil }),
				Access: contractAccess(func(context.Context, Plan, Facts) (Report, error) {
					return Report{{ID: "P09", Scope: op.Scope, Status: status, Mandatory: true,
						Expected: op.Action, Observed: reason}}, nil
				}),
				Prepare: contractPreparer(func(context.Context, Plan, Facts) (Prepared, error) {
					t.Fatal("missing selected template action must block before resource preparation")
					return Prepared{}, nil
				}),
			}
			_, err = pipeline.Run(t.Context(), plan, false, func(context.Context, Prepared) error {
				t.Fatal("missing selected action must not reach Foundry")
				return nil
			})
			require.Error(t, err)
		})
	}
}
