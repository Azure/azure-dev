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

func TestBYOPlanCopyLoops(t *testing.T) {
	template := map[string]any{
		"variables": map[string]any{
			"copy": []any{map[string]any{
				"name":  "clusterIds",
				"count": "[length(parameters('clusters'))]",
				"input": "[parameters('clusters')[copyIndex('clusterIds')]]",
			}},
		},
	}
	env := (&templatePlanResolver{}).environment(template, map[string]func() any{
		"clusters": func() any { return []any{offlineClusterID} },
	}, planScope{}, "root", "test")
	resolved := env.value(map[string]any{
		"allowProjectManagement": true,
		"copy": []any{map[string]any{
			"name":  "agentHostingConfigurations",
			"count": "[length(variables('clusterIds'))]",
			"input": map[string]any{
				"hostingType":       "ManagedCluster",
				"clusterResourceId": "[variables('clusterIds')[copyIndex('agentHostingConfigurations')]]",
				"indexPlusOne":      "[copyIndex('agentHostingConfigurations', 1)]",
			},
		}},
	})
	object, ok := resolved.(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, object, "copy")
	assert.Equal(t, []any{map[string]any{
		"hostingType":       "ManagedCluster",
		"clusterResourceId": offlineClusterID,
		"indexPlusOne":      float64(1),
	}}, object["agentHostingConfigurations"])

	unresolvedEnv := (&templatePlanResolver{}).environment(template, nil, planScope{}, "root", "test")
	unresolvedObj, ok := unresolvedEnv.value(map[string]any{"copy": []any{map[string]any{
		"name": "agentHostingConfigurations", "count": "[length(parameters('runtimeClusters'))]", "input": map[string]any{},
	}}}).(map[string]any)
	require.True(t, ok)
	assert.IsType(t, planUnknown{}, unresolvedObj["agentHostingConfigurations"])

	merged := env.value("[union(createObject('allowProjectManagement', true(), 'nested', createObject('a', 1)), " +
		"createObject('nested', createObject('b', 2), 'agentHostingConfigurations', createArray('aks')))]")
	assert.Equal(t, map[string]any{
		"allowProjectManagement":     true,
		"nested":                     map[string]any{"a": float64(1), "b": float64(2)},
		"agentHostingConfigurations": []any{"aks"},
	}, merged)
	assert.IsType(t, planUnknown{}, env.value("[union(createArray('a'), createArray('b'))]"))
	assert.Equal(t, "{1}-0", env.value("[format('{0}-{1}', '{1}', 0)]"))
	assert.IsType(t, planUnknown{}, env.value("[format('{0}', 1.5)]"))

	loopResolver := &templatePlanResolver{}
	loopEnv := loopResolver.environment(map[string]any{"resources": map[string]any{"acc": map[string]any{
		"type": "Microsoft.CognitiveServices/accounts", "name": "cog",
		"copy": map[string]any{"name": "accounts", "count": "[length(parameters('runtimeClusters'))]"},
	}}}, nil, planScope{}, "root", "test")
	loopResolver.walk(loopEnv, "", 0)
	assert.IsType(t, planUnknown{}, loopEnv.resources["acc"].env.value("[copyIndex('accounts')]"))
}
