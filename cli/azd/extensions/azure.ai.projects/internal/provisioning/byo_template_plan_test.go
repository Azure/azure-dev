// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
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
