// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoveryMissingNestedDeploymentRequiresExactFailedCreateValidationEvidence(t *testing.T) {
	const root = "/subscriptions/target/providers/Microsoft.Resources/deployments/attempt"
	const nested = "/subscriptions/target/resourceGroups/target-rg/providers/" +
		"Microsoft.Resources/deployments/foundry-resources"
	const prerequisites = "/subscriptions/target/resourceGroups/target-rg/providers/Microsoft.Resources/deployments/prereqs"
	const grant = "/subscriptions/target/resourceGroups/target-rg/providers/Microsoft.Authorization/roleAssignments/owned"
	for _, tc := range []struct {
		name, state, operation, status, errorCode, detailCode, parent string
		operationsHTTP, deploymentHTTP                                int
		failedRoot, message, wantSuccess                              bool
	}{
		{"validation rejected before registration", "Failed", "Create", "BadRequest",
			"InvalidTemplateDeployment", "InvalidResourceProperties", root, 404, 404, true, true, true},
		{"operations forbidden is not absent", "Failed", "Create", "BadRequest",
			"InvalidTemplateDeployment", "InvalidResourceProperties", root, 403, 404, true, true, false},
		{"unknown 404 is not absence", "Failed", "Create", "BadRequest",
			"InvalidTemplateDeployment", "InvalidResourceProperties", root, 404, 404, true, true, false},
		{"deployment forbidden is not absent", "Failed", "Create", "BadRequest",
			"InvalidTemplateDeployment", "InvalidResourceProperties", root, 404, 403, true, true, false},
		{"deployment actually exists", "Failed", "Create", "BadRequest",
			"InvalidTemplateDeployment", "InvalidResourceProperties", root, 404, 200, true, true, false},
		{"unknown root state", "Failed", "Create", "BadRequest",
			"InvalidTemplateDeployment", "InvalidResourceProperties", root, 404, 404, false, true, false},
		{"active operation", "Running", "Create", "BadRequest",
			"InvalidTemplateDeployment", "InvalidResourceProperties", root, 404, 404, true, true, false},
		{"non-create operation", "Failed", "Delete", "BadRequest",
			"InvalidTemplateDeployment", "InvalidResourceProperties", root, 404, 404, true, true, false},
		{"unknown HTTP status", "Failed", "Create", "Forbidden",
			"InvalidTemplateDeployment", "InvalidResourceProperties", root, 404, 404, true, true, false},
		{"runtime failure is not validation", "Failed", "Create", "BadRequest",
			"ResourceDeploymentFailure", "InvalidResourceProperties", root, 404, 404, true, true, false},
		{"unsupported inner error", "Failed", "Create", "BadRequest",
			"InvalidTemplateDeployment", "Unknown", root, 404, 404, true, true, false},
		{"unrelated parent operation", "Failed", "Create", "BadRequest",
			"InvalidTemplateDeployment", "InvalidResourceProperties", root + "-other", 404, 404, true, true, false},
		{"missing RP validation detail", "Failed", "Create", "BadRequest",
			"InvalidTemplateDeployment", "InvalidResourceProperties", root, 404, 404, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads []string
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method, "recovery remains read-only")
				reads = append(reads, request.URL.Path)
				status := http.StatusOK
				var body any
				switch strings.ToLower(request.URL.Path) {
				case strings.ToLower(root + "/operations"):
					message := "runtime resource failure"
					if tc.message {
						message = "The template deployment is not valid. The RP reported preflight validation errors."
					}
					body = map[string]any{"value": []any{
						map[string]any{"id": tc.parent + "/operations/failed", "properties": map[string]any{
							"provisioningOperation": tc.operation, "provisioningState": tc.state,
							"statusCode": tc.status,
							"statusMessage": map[string]any{"status": "Failed", "error": map[string]any{
								"code": tc.errorCode, "message": message,
								"details": []any{map[string]any{"code": tc.detailCode}},
							}},
							"targetResource": map[string]any{
								"id": nested, "resourceType": "Microsoft.Resources/deployments",
							},
						}},
						map[string]any{"properties": map[string]any{
							"provisioningState": "Succeeded",
							"targetResource": map[string]any{
								"id": prerequisites, "resourceType": "Microsoft.Resources/deployments",
							},
						}},
					}}
				case strings.ToLower(nested + "/operations"):
					status = tc.operationsHTTP
				case strings.ToLower(nested):
					status = tc.deploymentHTTP
				case strings.ToLower(prerequisites + "/operations"):
					body = map[string]any{"value": []any{map[string]any{"properties": map[string]any{
						"provisioningState": "Succeeded",
						"targetResource": map[string]any{
							"id": grant, "resourceType": "Microsoft.Authorization/roleAssignments",
						},
					}}}}
				default:
					t.Fatalf("unexpected recovery read %s", request.URL.Path)
				}
				if status != http.StatusOK {
					code := map[bool]string{true: "DeploymentNotFound", false: "AuthorizationFailed"}[status == 404]
					if tc.name == "unknown 404 is not absence" {
						code = "UnknownRoute"
					}
					body = map[string]any{"error": map[string]any{"code": code}}
				}
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				return contractARMResponse(request, status, string(raw)), nil
			})
			state := "Running"
			if tc.failedRoot {
				state = "Failed"
			}
			lifecycle := &ownedLifecycle{credential: contractCredential{}, options: options}
			succeeded := map[string]bool{}
			targets, err := lifecycle.deploymentGrantTargets(t.Context(), Plan{SubscriptionID: "target"}, &FoundryState{
				Attempts: []FoundryAttempt{{ID: root, Terminal: tc.failedRoot, ProvisioningState: state}},
			}, succeeded)
			if tc.wantSuccess {
				require.NoError(t, err)
				assert.Equal(t, map[string]bool{strings.ToLower(grant): true}, targets)
				assert.Equal(t, targets, succeeded, "only the actual successful sibling graph supplies grant evidence")
				assert.True(t, slices.ContainsFunc(reads, func(path string) bool { return strings.EqualFold(path, nested) }))
			} else {
				require.Error(t, err)
				assert.Empty(t, targets, "unverified nested branch must not authorize grant adoption")
			}
		})
	}
}
