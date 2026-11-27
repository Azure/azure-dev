// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBYOPlanNestedDeploymentName(t *testing.T) {
	scope := planScope{subscriptionID: "sub", subscriptionARMID: "/subscriptions/sub",
		group: "rg", groupARMID: "/subscriptions/sub/resourceGroups/rg", groupExists: true}
	for _, name := range []string{"inner", "[parameters('runtimeName')]"} {
		t.Run(name, func(t *testing.T) {
			resolver := &templatePlanResolver{scopes: map[string]planScope{"sub/rg": scope}}
			template := map[string]any{"resources": map[string]any{"nested": map[string]any{
				"type": "Microsoft.Resources/deployments", "name": name,
				"properties": map[string]any{
					"expressionEvaluationOptions": map[string]any{"scope": "inner"},
					"template": map[string]any{"resources": map[string]any{"identity": map[string]any{
						"type": "Microsoft.ManagedIdentity/userAssignedIdentities", "name": "[deployment().name]",
					}}},
				},
			}}}
			env := resolver.environment(template, nil, scope, "root", "outer")
			resolver.walk(env, "", 0)
			child := env.resources["nested"].module()
			require.NotNil(t, child)
			id, missing := child.resources["identity"].id()
			if name == "inner" {
				assert.Equal(t, scope.groupARMID+"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/inner", id)
				assert.Empty(t, missing)
			} else {
				assert.Empty(t, id)
				assert.Contains(t, missing, "runtimeName")
			}
			assert.Equal(t, "outer", planField(env.value("[deployment()]"), "name"))
		})
	}
}

func TestBYOPlanConsumedParameters(t *testing.T) {
	const hostingID = suppliedGroupID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/custom-hosting"
	const workloadID = suppliedGroupID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/custom-workload"
	const storageID = suppliedGroupID + "/providers/Microsoft.Storage/storageAccounts/storage"

	tests := []struct {
		name            string
		rootParamDefs   map[string]any
		rootResources   map[string]any
		variables       map[string]any
		childParams     map[string]any
		childParamDefs  map[string]any
		resources       map[string]any
		childOutputs    map[string]any
		outputs         map[string]any
		parameters      map[string]any
		wantHostingReq  bool
		wantWorkloadReq bool
	}{
		{
			name:      "unused, unselected field, array minLength, inactive outputs, and inactive module",
			variables: map[string]any{"unused": "[parameters('forwardedHostingPrincipal')]"},
			rootResources: map[string]any{"inactiveMod": map[string]any{
				"condition": "[false()]", "type": "Microsoft.Resources/deployments",
				"name": "inactive-mod", "subscriptionId": "00000000-0000-0000-0000-000000000099",
				"properties": map[string]any{"template": map[string]any{
					"languageVersion": "2.0", "resources": map[string]any{},
					"outputs": map[string]any{"x": map[string]any{"type": "string", "value": "unused"}},
				}},
			}},
			childParams: map[string]any{
				"config": map[string]any{"value": map[string]any{
					"name": "selected-name", "principalId": "[parameters('hostingPrincipalId')]",
				}},
				"principals": map[string]any{"value": []any{"[parameters('workloadPrincipalId')]"}},
			},
			childParamDefs: map[string]any{
				"config":     map[string]any{"type": "object"},
				"principals": map[string]any{"type": "array", "minLength": 1.0},
			},
			resources: map[string]any{
				"nameReader": map[string]any{
					"type": "Microsoft.Authorization/roleAssignments", "name": "[parameters('config').name]",
				},
				"disabled": map[string]any{
					"condition":  "[and(not(empty(parameters('forwardedHostingPrincipal'))), false())]",
					"type":       "Microsoft.Authorization/roleAssignments",
					"name":       "disabled",
					"properties": map[string]any{"principalId": "[parameters('forwardedHostingPrincipal')]"},
				},
			},
			childOutputs: map[string]any{
				"activeOut": map[string]any{"type": "string", "value": "ok"},
				"deadOut": map[string]any{
					"condition": "[false()]", "type": "string", "value": "[parameters('forwardedHostingPrincipal')]",
				},
			},
			outputs: map[string]any{
				"fromChild": map[string]any{
					"type": "string", "value": "[reference('customDeployment').outputs.activeOut.value]",
				},
				"speculativeInactive": map[string]any{
					"type":  "string",
					"value": "[if(greater(0, 1), reference('inactiveMod').outputs.x.value, 'ok')]",
				},
				"deadRoot": map[string]any{
					"condition": "[false()]", "type": "string", "value": "[parameters('workloadPrincipalId')]",
				},
			},
		},
		{
			name: "selected forwarded object field",
			childParams: map[string]any{"config": map[string]any{"value": map[string]any{
				"name": "selected-name", "principalId": "[parameters('hostingPrincipalId')]",
			}}},
			childParamDefs: map[string]any{"config": map[string]any{"type": "object"}},
			resources: map[string]any{"grant": map[string]any{
				"type": "Microsoft.Authorization/roleAssignments", "name": "grant",
				"properties": map[string]any{"principalId": "[parameters('config').principalId]"},
			}},
			wantHostingReq: true,
		},
		{
			name: "unknown-condition and false-condition child outputs trigger coalesce fallback",
			childOutputs: map[string]any{
				"maybeOut": map[string]any{
					"condition": "[contains(parameters('existingKeys'), 'k')]",
					"type":      "string",
					"value":     "22222222-2222-2222-2222-222222222222",
				},
				"falseOut": map[string]any{"condition": "[false()]", "type": "string", "value": "unused"},
			},
			outputs: map[string]any{
				"unknownFallback": map[string]any{
					"type": "string",
					"value": "[coalesce(tryGet(reference('customDeployment').outputs, 'maybeOut', 'value'), " +
						"parameters('hostingPrincipalId'))]",
				},
				"falseFallback": map[string]any{
					"type": "string",
					"value": "[coalesce(tryGet(reference('customDeployment').outputs, 'falseOut', 'value'), " +
						"parameters('workloadPrincipalId'))]",
				},
			},
			wantHostingReq:  true,
			wantWorkloadReq: true,
		},
		{
			name:          "no-default root param and string minLength child param",
			rootParamDefs: map[string]any{"hostingPrincipalId": map[string]any{"type": "string"}},
			childParams:   map[string]any{"validated": map[string]any{"value": "[parameters('workloadPrincipalId')]"}},
			childParamDefs: map[string]any{
				"validated": map[string]any{"type": "string", "minLength": 1.0},
			},
			wantHostingReq:  true,
			wantWorkloadReq: true,
		},
		{
			name: "expression-empty root default with minLength requires principal",
			rootParamDefs: map[string]any{
				"hostingPrincipalId": map[string]any{"type": "string", "defaultValue": "[concat('')]", "minLength": 1.0},
			},
			wantHostingReq: true,
		},
		{
			name:       "kubelet nonEmpty folds empty checks and truncates coalesce",
			parameters: map[string]any{"includeAcr": map[string]any{"value": true}},
			resources: map[string]any{"kubeletGrant": map[string]any{
				"type": "Microsoft.Authorization/roleAssignments", "name": "kubelet-grant",
				"properties": map[string]any{
					"principalId": "[if(not(empty(parameters('kubeletId'))), " +
						"parameters('forwardedWorkloadPrincipal'), '11111111-1111-1111-1111-111111111111')]",
					"deadEmpty": "[if(empty(parameters('kubeletId')), parameters('forwardedHostingPrincipal'), 'ok')]",
					"truncated": "[coalesce(parameters('kubeletId'), parameters('forwardedHostingPrincipal'))]",
				},
			}},
			wantWorkloadReq: true,
		},
		{
			name: "runtime unknown if cached during walk and nullable coalesce fallback",
			variables: map[string]any{
				"runtimeCond": "[if(equals(reference('customStorage').kind, 'StorageV2'), " +
					"'11111111-1111-1111-1111-111111111111', parameters('forwardedWorkloadPrincipal'))]",
			},
			resources: map[string]any{
				"walkCachedCondition": map[string]any{
					"condition": "[not(empty(variables('runtimeCond')))]",
					"type":      "Microsoft.Authorization/roleAssignments", "name": "cond-only",
				},
				"nullableFallback": map[string]any{
					"type": "Microsoft.Authorization/roleAssignments", "name": "coalesce-grant",
					"properties": map[string]any{
						"principalId": "[coalesce(tryGet(reference('customStorage'), 'secondaryEndpoints', 'blob'), " +
							"parameters('forwardedHostingPrincipal'))]",
					},
				},
			},
			wantHostingReq:  true,
			wantWorkloadReq: true,
		},
		{
			name: "unresolved dynamic tag key",
			resources: map[string]any{"tagged": map[string]any{
				"type": "Microsoft.Authorization/roleAssignments", "name": "tagged",
				"tags": map[string]any{"[uniqueString('tag-only')]": "[parameters('forwardedHostingPrincipal')]"},
			}},
			wantHostingReq: true,
		},
		{
			name:       "explicitly supplied parameter disables automatic requirement",
			parameters: map[string]any{"HostingPrincipalID": map[string]any{"value": suppliedHostingPrincipal}},
			resources: map[string]any{"grant": map[string]any{
				"type": "Microsoft.Authorization/roleAssignments", "name": "grant",
				"properties": map[string]any{"principalId": "[parameters('forwardedHostingPrincipal')]"},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, fake, _ := suppliedProvider(t, 0)
			template := customBYOIdentityPlanTemplate(
				hostingID, workloadID, storageID,
				"[reference('customHosting').principalId]",
				"[reference('customWorkload').principalId]",
			)
			maps.Copy(template["parameters"].(map[string]any), tt.rootParamDefs)
			rootResources := template["resources"].(map[string]any)
			maps.Copy(rootResources, tt.rootResources)
			props := rootResources["customDeployment"].(map[string]any)["properties"].(map[string]any)
			propsParams := props["parameters"].(map[string]any)
			propsParams["kubeletId"] = map[string]any{"value": "[parameters('kubeletObjectId')]"}
			propsParams["existingKeys"] = map[string]any{"value": "[parameters('byoAksExistingRoleAssignmentKeys')]"}
			maps.Copy(propsParams, tt.childParams)
			inner := props["template"].(map[string]any)
			innerParams := inner["parameters"].(map[string]any)
			innerParams["kubeletId"] = map[string]any{"type": "string", "defaultValue": ""}
			innerParams["existingKeys"] = map[string]any{"type": "array", "defaultValue": []any{}}
			maps.Copy(innerParams, tt.childParamDefs)
			inner["variables"] = tt.variables
			inner["outputs"] = tt.childOutputs
			maps.Copy(inner["resources"].(map[string]any), tt.resources)
			maps.Copy(template["outputs"].(map[string]any), tt.outputs)

			plan, err := p.identifyBYOTemplatePlan(t.Context(), &templateSource{
				mode: templateModeBicep, armTemplate: template, parameters: tt.parameters,
			}, nil)
			require.NoError(t, err)
			assert.NotContains(t, fake.requests, "GET /subscriptions/00000000-0000-0000-0000-000000000099")
			assert.Equal(t, hostingID, plan.Hosting.ID)
			assert.Equal(t, workloadID, plan.Workload.ID)
			assert.False(t, plan.Hosting.Supplied)
			assert.False(t, plan.Workload.Supplied)
			assert.Equal(t, tt.wantHostingReq, plan.Hosting.PrincipalRequired)
			assert.Equal(t, tt.wantWorkloadReq, plan.Workload.PrincipalRequired)
		})
	}
}
