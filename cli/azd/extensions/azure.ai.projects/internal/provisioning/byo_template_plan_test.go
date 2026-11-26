// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"azure.ai.projects/synthesis/testutil"

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
}

func assertModuleVariantDetection(t *testing.T, tmpl map[string]any) {
	t.Helper()
	p := &FoundryProvisioningProvider{subID: "target-sub"}
	for _, tc := range []struct {
		name      string
		enableAks bool
		mode      string
		wantBYO   bool
	}{
		{name: "native variant and false if branch", enableAks: false, mode: "native", wantBYO: false},
		{name: "safe native variant", enableAks: false, mode: "safeNative", wantBYO: false},
		{name: "true if branch", enableAks: true, mode: "native", wantBYO: true},
		{name: "nested module union BYO", enableAks: false, mode: "moduleByo", wantBYO: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, planErr := p.localBYOPlan(&templateSource{
				mode:        templateModeBicep,
				armTemplate: tmpl,
				parameters: map[string]any{
					"enableAks": map[string]any{"value": tc.enableAks},
					"mode":      map[string]any{"value": tc.mode},
				},
			})
			require.NoError(t, planErr)
			assert.Equal(t, tc.wantBYO, got)
		})
	}
}

func TestBYOModuleConditionalParameterEnvelopeAndVariantDetection(t *testing.T) {
	hostingExpr := "createObject('agentHostingConfigurations', createArray(createObject(" +
		"'hostingType', 'ManagedCluster', 'clusterResourceId', '" + offlineClusterID + "', " +
		"'storageAccountResourceId', '" + suppliedStorageID + "', " +
		"'hostingManagementIdentityResourceId', '" + suppliedHostingID + "', " +
		"'workloadIdentityResourceId', '" + suppliedWorkloadID + "')))"
	rgRef := "reference(subscriptionResourceId('Microsoft.Resources/resourceGroups', 'target-rg'), '2021-04-01')"
	variantsExpr := "createObject('native', " + rgRef + ", 'byo', " + hostingExpr + ")"
	paramExpr := "[if(equals(parameters('mode'), 'moduleByo'), " +
		"createObject('value', union(" + rgRef + ", " + hostingExpr + ")), " +
		"if(equals(parameters('mode'), 'safeNative'), " +
		"createObject('value', tryGet(" + variantsExpr + ", 'native')), " +
		"if(parameters('enableAks'), " +
		"createObject('value', union(" + hostingExpr + ", " + rgRef + ")), " +
		"createObject('value', " + variantsExpr + ".native))))]"
	assertModuleVariantDetection(t, map[string]any{
		"parameters": map[string]any{
			"enableAks": map[string]any{"type": "bool", "defaultValue": false},
			"mode":      map[string]any{"type": "string", "defaultValue": "native"},
		},
		"resources": []any{map[string]any{
			"type": "Microsoft.Resources/deployments", "name": "mod", "resourceGroup": "target-rg",
			"properties": map[string]any{
				"expressionEvaluationOptions": map[string]any{"scope": "inner"},
				"parameters":                  map[string]any{"accountProps": paramExpr},
				"template": map[string]any{
					"parameters": map[string]any{"accountProps": map[string]any{"type": "object"}},
					"resources": []any{map[string]any{
						"type": "Microsoft.CognitiveServices/accounts", "name": "child-acc",
						"properties": "[parameters('accountProps')]",
					}},
				},
			},
		}},
	})
}

func TestBYOCompiledBicepModuleIfAndVariantDetection(t *testing.T) {
	tool := testutil.LookupBicep(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "child.bicep"), []byte(`
param accountProps object
resource acc 'Microsoft.CognitiveServices/accounts@2026-07-15-preview' = {
  name: 'child-acc'
  location: 'westus'
  sku: { name: 'S0' }
  kind: 'AIServices'
  properties: accountProps
}
`), 0o600))
	hostingBlock := `{
    agentHostingConfigurations: [{
      hostingType: 'ManagedCluster'
      clusterResourceId: '` + offlineClusterID + `'
      storageAccountResourceId: '` + suppliedStorageID + `'
      hostingManagementIdentityResourceId: '` + suppliedHostingID + `'
      workloadIdentityResourceId: '` + suppliedWorkloadID + `'
    }]
  }`
	mainPath := filepath.Join(dir, "main.bicep")
	require.NoError(t, os.WriteFile(mainPath, []byte(`
targetScope = 'subscription'
param enableAks bool = false
param mode string = 'native'

resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' existing = {
  name: 'target-rg'
}

var variants = {
  native: any(rg.properties)
  byo: `+hostingBlock+`
}

module mod './child.bicep' = {
  name: 'mod'
  scope: rg
  params: {
    accountProps: mode == 'moduleByo'
      ? union(any(rg.properties), `+hostingBlock+`)
      : (mode == 'safeNative'
          ? variants.?native
          : (enableAks
              ? union(`+hostingBlock+`, any(rg.properties))
              : variants.native))
  }
}
`), 0o600))

	//nolint:gosec // Test-owned Bicep fixture under t.TempDir.
	cmd := exec.CommandContext(t.Context(), tool, "build", mainPath, "--stdout")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	compiled, err := cmd.Output()
	require.NoError(t, err, stderr.String())
	tmpl, err := unmarshalARMTemplate(string(compiled), mainPath)
	require.NoError(t, err)
	assertModuleVariantDetection(t, tmpl)
}
