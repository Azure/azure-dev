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
		resources       map[string]any
		variables       map[string]any
		childOutputs    map[string]any
		outputs         map[string]any
		parameters      map[string]any
		wantHostingReq  bool
		wantWorkloadReq bool
	}{
		{
			name: "unused declaration and inactive child or root outputs do not require principal",
			variables: map[string]any{
				"unusedPrincipal": "[parameters('forwardedHostingPrincipal')]",
			},
			childOutputs: map[string]any{
				"activeOut": map[string]any{
					"type":  "string",
					"value": "ok",
				},
				"inactiveChildOutput": map[string]any{
					"condition": "[and(not(empty(parameters('forwardedHostingPrincipal'))), false())]",
					"type":      "string",
					"value":     "[parameters('forwardedHostingPrincipal')]",
				},
			},
			outputs: map[string]any{
				"fromChild": map[string]any{
					"type":  "string",
					"value": "[reference('customDeployment').outputs.activeOut.value]",
				},
				"inactiveOutput": map[string]any{
					"condition": "[and(not(empty(parameters('workloadPrincipalId'))), false())]",
					"type":      "string",
					"value":     "[parameters('workloadPrincipalId')]",
				},
			},
			wantHostingReq:  false,
			wantWorkloadReq: false,
		},
		{
			name: "active child and root outputs record consumption",
			childOutputs: map[string]any{
				"workloadPrincipal": map[string]any{
					"type":  "string",
					"value": "[parameters('forwardedWorkloadPrincipal')]",
				},
			},
			outputs: map[string]any{
				"hostingPrincipal": map[string]any{
					"type":  "string",
					"value": "[parameters('hostingPrincipalId')]",
				},
			},
			wantHostingReq:  true,
			wantWorkloadReq: true,
		},
		{
			name: "explicitly supplied parameter disables automatic principal requirement",
			parameters: map[string]any{
				"HostingPrincipalID": map[string]any{"value": suppliedHostingPrincipal},
			},
			resources: map[string]any{
				"grant": map[string]any{
					"type": "Microsoft.Authorization/roleAssignments",
					"name": "grant",
					"properties": map[string]any{
						"principalId": "[parameters('forwardedHostingPrincipal')]",
					},
				},
			},
			wantHostingReq:  false,
			wantWorkloadReq: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _, _ := suppliedProvider(t, 0)
			template := customBYOIdentityPlanTemplate(hostingID, workloadID, storageID, "ref", "ref")
			rootResources, ok := template["resources"].(map[string]any)
			require.True(t, ok)
			deployment, ok := rootResources["customDeployment"].(map[string]any)
			require.True(t, ok)
			props, ok := deployment["properties"].(map[string]any)
			require.True(t, ok)
			inner, ok := props["template"].(map[string]any)
			require.True(t, ok)
			innerResources, ok := inner["resources"].(map[string]any)
			require.True(t, ok)
			rootOutputs, ok := template["outputs"].(map[string]any)
			require.True(t, ok)
			inner["variables"] = tt.variables
			inner["outputs"] = tt.childOutputs
			maps.Copy(innerResources, tt.resources)
			maps.Copy(rootOutputs, tt.outputs)

			source := &templateSource{
				mode:        templateModeBicep,
				armTemplate: template,
				parameters:  tt.parameters,
			}
			plan, err := p.identifyBYOTemplatePlan(t.Context(), source, nil)
			require.NoError(t, err)
			assert.False(t, plan.Hosting.Supplied)
			assert.False(t, plan.Workload.Supplied)
			assert.Equal(t, tt.wantHostingReq, plan.Hosting.PrincipalRequired)
			assert.Equal(t, tt.wantWorkloadReq, plan.Workload.PrincipalRequired)
		})
	}
}
