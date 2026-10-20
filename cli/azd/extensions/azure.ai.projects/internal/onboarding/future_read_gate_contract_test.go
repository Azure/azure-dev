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

func TestFutureReadPermissionsUseExistingParentAndCloseWriteGate(t *testing.T) {
	for _, existingGroup := range []bool{false, true} {
		t.Run(fmt.Sprint(existingGroup), func(t *testing.T) {
			input := contractPlanInput()
			input.StorageID, input.RegistryID = "", ""
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			subscription := "/subscriptions/" + plan.SubscriptionID
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method, "authorization must not test permissions with writes")
				var body string
				switch {
				case strings.Contains(request.URL.Path, "/denyAssignments"):
					body = `{"value":[]}`
				case strings.Contains(request.URL.Path, "/roleAssignments"):
					body = fmt.Sprintf(`{"value":[{"properties":{
						"scope":%q,"principalId":"operator","principalType":"User",
						"roleDefinitionId":"/providers/Microsoft.Authorization/roleDefinitions/custom"}}]}`, subscription)
				case strings.Contains(request.URL.Path, "/roleDefinitions"):
					body = `{"properties":{"permissions":[{"actions":["*"],"notActions":[
						"Microsoft.Storage/storageAccounts/read","Microsoft.ContainerRegistry/registries/read",
						"Microsoft.Resources/subscriptions/resourceGroups/read"]}]}}`
				default:
					require.Equal(t, strings.ToLower(subscription+"/resourceGroups/"+plan.ResourceGroup),
						strings.ToLower(request.URL.Path), "no future-resource GET is an authorization probe")
					if !existingGroup {
						return contractARMResponse(request, 404, `{"error":{"code":"ResourceGroupNotFound"}}`), nil
					}
					body = `{"id":"` + request.URL.Path + `","location":"eastus"}`
				}
				return contractARMResponse(request, 200, body), nil
			})
			resolved, err := ResolveExistingParents(t.Context(), plan, contractCredential{}, options)
			require.NoError(t, err)
			parent := subscription
			if existingGroup {
				parent += "/resourceGroups/" + plan.ResourceGroup
			}
			auth := &scopedAuthorizer{credential: contractCredential{}, options: onboardingARMOptions(options)}
			var report Report
			for _, action := range []string{
				"Microsoft.Storage/storageAccounts/read", "Microsoft.ContainerRegistry/registries/read",
				"Microsoft.Resources/subscriptions/resourceGroups/read",
			} {
				index := slices.IndexFunc(resolved.Operations, func(op Operation) bool { return op.Action == action })
				require.NotEqual(t, -1, index)
				op := resolved.Operations[index]
				assert.Equal(t, parent, op.Scope)
				status, reason, err := auth.evaluate(t.Context(), callerIdentity{objectID: "operator"}, op)
				require.NoError(t, err)
				require.Equal(t, Fail, status, reason)
				report = append(report, Check{ID: op.CheckID, Scope: op.Scope, Status: status, Mandatory: true,
					Expected: action, Observed: reason})
				write := op
				write.Action = strings.TrimSuffix(action, "/read") + "/write"
				status, reason, err = auth.evaluate(t.Context(), callerIdentity{objectID: "operator"}, write)
				require.NoError(t, err)
				require.Equal(t, Pass, status, reason)
			}
			pipeline := Pipeline{
				Resources: contractResources(func(context.Context, Plan) (Facts, Report, error) { return Facts{}, nil, nil }),
				Access: contractAccess(func(context.Context, Plan, Facts) (Report, error) {
					return report, report.Err()
				}),
				Prepare: contractPreparer(func(context.Context, Plan, Facts) (Prepared, error) {
					t.Fatal("create permission without readback permission must not start preparation")
					return Prepared{}, nil
				}),
			}
			_, err = pipeline.Run(t.Context(), resolved, false, func(context.Context, Prepared) error {
				t.Fatal("missing preparation read permissions must not reach Foundry")
				return nil
			})
			require.Error(t, err)
		})
	}
}
