// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"azure.ai.projects/internal/onboarding"

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

func TestBYOPlanReferencePreservesAncestorScopeGrants(t *testing.T) {
	scope := planScope{subscriptionID: "sub", subscriptionARMID: "/subscriptions/sub",
		group: "rg", groupARMID: "/subscriptions/sub/resourceGroups/rg", groupExists: true}
	for _, selector := range []string{
		"reference(resourceId('Microsoft.ManagedIdentity/userAssignedIdentities', 'hosting'), '2023-01-31').principalId",
		"reference(resourceId('Microsoft.ManagedIdentity/userAssignedIdentities', 'hosting'), '2023-01-31', 'Full')." +
			"properties.principalId",
	} {
		t.Run(selector, func(t *testing.T) {
			resolver := &templatePlanResolver{scopes: map[string]planScope{"sub/rg": scope},
				references: map[*planResource]bool{}}
			template := map[string]any{"resources": map[string]any{
				"hosting": map[string]any{
					"type": "Microsoft.ManagedIdentity/userAssignedIdentities", "name": "hosting",
				},
				"grant": map[string]any{
					"type": "Microsoft.Authorization/roleAssignments", "name": "grant",
					"properties": map[string]any{
						"principalId": "[" + selector + "]",
						"roleDefinitionId": scope.subscriptionARMID + "/providers/Microsoft.Authorization/" +
							"roleDefinitions/acdd72a7-3385-48ef-bd42-f606fba81ae7",
					},
				},
			}}
			env := resolver.environment(template, nil, scope, "root", "deployment")
			resolver.walk(env, "", 0)
			hostingID, missing := env.resources["hosting"].id()
			require.Empty(t, missing)
			plan := onboarding.Plan{Hosting: onboarding.Resource{ID: hostingID},
				Cluster: onboarding.Resource{ID: offlineClusterID}}
			resolver.bindGrants(&plan)
			require.NoError(t, resolver.err)
			require.Len(t, plan.Grants, 1)
			assert.Equal(t, "G01", plan.Grants[0].CheckID)
			assert.Equal(t, scope.groupARMID, plan.Grants[0].AuthorityScope)
			assert.Empty(t, plan.Grants[0].Missing)
			assert.Equal(t, hostingID, planField(env.value("[reference('hosting', '2023-01-31', 'Full')]"), "id"))
			assert.IsType(t, planUnknown{}, env.value("[reference('hosting', '2023-01-31').id]"))
		})
	}
}
