// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"fmt"
	"strings"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBYONonBooleanIncludeAcrReachesARM(t *testing.T) {
	for _, preview := range []bool{false, true} {
		for _, value := range []any{"true", "false", "[equals(1, 1)]", 1.0, nil} {
			t.Run(fmt.Sprintf("preview=%t/value=%v", preview, value), func(t *testing.T) {
				p, fake := offlineBYOProvider(t)
				p.onDiskSource = &templateSource{
					mode: templateModeBicep, armTemplate: p.armTemplate,
					parameters: map[string]any{"includeAcr": map[string]any{"value": value}},
				}
				var err error
				if preview {
					_, err = p.Preview(t.Context(), func(string) {})
				} else {
					_, err = p.Deploy(t.Context(), func(string) {})
				}
				require.ErrorContains(t, err, "InvalidTemplate")
				require.ErrorContains(t, err, "offline ARM includeAcr type validation")
				assert.Equal(t, map[string]any{"value": value}, fake.parameters["includeAcr"])
				assert.NotContains(t, fake.parameters, "kubeletObjectId")
				assert.Contains(t, fake.requests, "GET "+offlineClusterID)
				assert.Zero(t, fake.writes)
				assert.Zero(t, fake.previews)
			})
		}
	}
}

func TestBYOPreparePreservesSuppliedKubeletRegardlessOfACR(t *testing.T) {
	for _, includeAcr := range []bool{false, true} {
		for _, test := range []struct {
			name      string
			parameter any
			duplicate bool
		}{
			{"empty", map[string]any{"value": ""}, false},
			{"whitespace", map[string]any{"value": " "}, false},
			{"non-string", map[string]any{"value": 1}, false},
			{"null", map[string]any{"value": nil}, false},
			{"reference", map[string]any{"reference": map[string]any{"secretName": "kubelet"}}, false},
			{"duplicate", map[string]any{"value": offlineKubeletObjectID}, true},
			{"different", map[string]any{"value": suppliedHostingPrincipal}, false},
			{"matching", map[string]any{"value": strings.ToUpper(offlineKubeletObjectID)}, false},
		} {
			t.Run(fmt.Sprintf("includeAcr=%t/%s", includeAcr, test.name), func(t *testing.T) {
				p, fake := offlineBYOProvider(t)
				source, err := p.resolveTemplate(t.Context(), func(string) {})
				require.NoError(t, err)
				delete(source.parameters, "includeAcr")
				source.parameters["INCLUDEACR"] = map[string]any{"value": includeAcr}
				source.parameters["kubeletObjectId"] = test.parameter
				if test.duplicate {
					source.parameters["KUBELETOBJECTID"] = test.parameter
				}
				require.NoError(t, p.prepareBYO(t.Context(), source, func(string) {}))
				assert.Contains(t, fake.requests, "GET "+offlineClusterID)
				assert.Equal(t, test.parameter, source.parameters["kubeletObjectId"])
				if test.duplicate {
					assert.Equal(t, test.parameter, source.parameters["KUBELETOBJECTID"])
				}
				assert.Equal(t, map[string]any{"value": includeAcr}, source.parameters["INCLUDEACR"])
			})
		}
	}
}

func TestBYOSuppliedBindingsPreserveExplicitValues(t *testing.T) {
	for _, binding := range []string{"hostingPrincipalId", "workloadPrincipalId", "federatedIdentityRoleDefinitionId"} {
		for _, declared := range []bool{false, true} {
			for _, preview := range []bool{false, true} {
				for _, parameter := range []any{
					map[string]any{"value": "explicit-" + binding},
					map[string]any{"value": ""},
					map[string]any{"reference": map[string]any{"secretName": binding}},
				} {
					t.Run(fmt.Sprintf("%s/declared=%t/preview=%t/%v", binding, declared, preview, parameter),
						func(t *testing.T) {
							p, fake, _ := suppliedProvider(t, 7)
							source, err := p.resolveTemplate(t.Context(), func(string) {})
							require.NoError(t, err)
							parameterName := strings.ToUpper(binding)
							source.parameters[parameterName] = parameter
							if !declared {
								definitions, ok := source.armTemplate["parameters"].(map[string]any)
								require.True(t, ok)
								delete(definitions, binding)
							}
							p.onDiskSource = source
							if preview {
								_, err = p.Preview(t.Context(), func(string) {})
							} else {
								_, err = p.Deploy(t.Context(), func(string) {})
							}
							if declared {
								require.NoError(t, err)
							} else {
								require.ErrorContains(t, err, "InvalidTemplate")
								require.ErrorContains(t, err, "offline ARM undeclared parameter: "+parameterName)
								assert.Zero(t, fake.base.writes)
								assert.Zero(t, fake.base.previews)
							}
							assert.Equal(t, parameter, fake.base.parameters[parameterName])
							assert.NotContains(t, fake.base.parameters, binding)
							assert.Contains(t, fake.requests, "GET "+offlineClusterID)
						})
				}
			}
		}
	}
}

func TestBYOSuppliedUsesEffectiveDeploymentInputs(t *testing.T) {
	for _, name := range []string{"parameters", "template default", "without YAML", "disabled", "undeclared"} {
		t.Run(name, func(t *testing.T) {
			p, fake, _ := suppliedProvider(t, 7)
			source, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			clusterID := offlineClusterID + "-effective"
			fake.base.clusterID = clusterID
			hosting := map[string]any{
				"enabled": true, "hostingType": "ManagedCluster", "name": "custom",
				"clusterResourceId": clusterID, "storageAccountResourceId": suppliedStorageID,
				"hostingManagementIdentityResourceId": suppliedHostingID,
				"workloadIdentityResourceId":          suppliedWorkloadID,
				"customSetting":                       true,
			}
			delete(source.parameters, "agentHosting")
			source.parameters["AGENTHOSTING"] = map[string]any{"value": hosting}
			definitions, ok := source.armTemplate["parameters"].(map[string]any)
			require.True(t, ok)
			switch name {
			case "template default", "without YAML":
				result, err := synthesis.Synthesize(synthesis.Input{ServiceName: "project",
					RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n")})
				require.NoError(t, err)
				p.synthResult = result
				if name == "template default" {
					delete(source.parameters, "AGENTHOSTING")
					definition, ok := definitions["agentHosting"].(map[string]any)
					require.True(t, ok)
					definition["defaultValue"] = hosting
				}
			case "disabled":
				hosting["enabled"], hosting["clusterResourceId"] = false, 42
			case "undeclared":
				delete(definitions, "agentHosting")
				source.parameters["AGENTHOSTING"] = map[string]any{"value": "leave undeclared parameters to ARM"}
			}
			require.NoError(t, p.prepareBYO(t.Context(), source, func(string) {}))
			assert.NotContains(t, source.parameters, "agentHosting")
			if name == "disabled" || name == "undeclared" {
				assert.Empty(t, fake.requests)
				assert.NotContains(t, source.parameters, "federatedIdentityRoleDefinitionId")
			} else {
				assert.Contains(t, fake.requests, "GET "+clusterID)
				assert.NotContains(t, fake.requests, "GET "+offlineClusterID)
				assert.Equal(t, map[string]any{"value": suppliedHostingPrincipal}, source.parameters["hostingPrincipalId"])
				assert.Equal(t, map[string]any{"value": suppliedWorkloadPrincipal}, source.parameters["workloadPrincipalId"])
				if name == "template default" {
					assert.NotContains(t, source.parameters, "AGENTHOSTING")
				} else {
					assert.Equal(t, map[string]any{"value": hosting}, source.parameters["AGENTHOSTING"])
				}
			}
		})
	}
}

func TestBYOPlanOptionalSupportingSelections(t *testing.T) {
	for _, value := range []any{nil, "", suppliedStorageID} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			p, _ := offlineBYOProvider(t)
			env := (&templatePlanResolver{}).environment(p.armTemplate, map[string]func() any{
				"agenthosting": func() any { return map[string]any{"storageAccountResourceId": value} },
			}, planScope{}, "root", "test")
			result := env.value("[coalesce(tryGet(parameters('agentHosting'), 'storageAccountResourceId'), '')]")
			expected := value
			if expected == nil {
				expected = ""
			}
			assert.Equal(t, expected, result)
			assert.Equal(t, "", env.value("[coalesce(tryGet(parameters('agentHosting'), 'missing'), '')]"))
			assert.IsType(t, planUnknown{}, env.value("[coalesce(parameters('unknown'), 'fallback')]"))
			assert.Equal(t, "known", env.value("[coalesce('known', parameters('unknown'))]"))
		})
	}
}

func TestBYOSuppliedAllowsIdenticalIdentities(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 7)
	result, err := synthesis.Synthesize(synthesis.Input{ServiceName: "project",
		RawAzureYAML: []byte(strings.Replace(suppliedYAML(7), suppliedWorkloadID, suppliedHostingID, 1))})
	require.NoError(t, err)
	p.synthResult = result
	_, err = p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, 1, fake.base.writes)
	for _, binding := range []string{"hostingPrincipalId", "workloadPrincipalId"} {
		assert.Equal(t, map[string]any{"value": suppliedHostingPrincipal}, fake.base.parameters[binding])
	}
}

func TestBYOSuppliedPreflightUsesCustomizedCompiledPlan(t *testing.T) {
	p, fake, compiler := suppliedProvider(t, 7)
	p.armTemplate["resources"] = map[string]any{"customDeployment": map[string]any{
		"type": "Microsoft.Resources/deployments", "name": "custom-deployment", "resourceGroup": "custom-rg",
		"properties": map[string]any{
			"expressionEvaluationOptions": map[string]any{"scope": "inner"},
			"template": map[string]any{"resources": map[string]any{"customAccount": map[string]any{
				"type": "Microsoft.CognitiveServices/accounts", "name": "custom-account",
				"properties": map[string]any{"agentHostingConfigurations": []any{map[string]any{
					"hostingType": "ManagedCluster", "clusterResourceId": offlineClusterID,
					"storageAccountResourceId":            suppliedStorageID,
					"hostingManagementIdentityResourceId": suppliedHostingID,
					"workloadIdentityResourceId":          suppliedWorkloadID,
				}}},
			}}},
		},
	}}
	p.onDiskSource = &templateSource{
		mode: templateModeBicep, armTemplate: p.armTemplate,
		parameters: map[string]any{"includeAcr": map[string]any{"value": true}},
	}
	source, err := p.resolveTemplate(t.Context(), func(string) {})
	require.NoError(t, err)
	plan, err := p.identifyBYOTemplatePlan(t.Context(), source)
	require.NoError(t, err)
	assert.Equal(t, suppliedStorageID, plan.Storage.ID)
	assert.True(t, plan.Storage.Supplied)
	var progress strings.Builder
	_, err = p.Deploy(t.Context(), func(message string) { progress.WriteString(message) })
	require.NoError(t, err, progress.String())
	assert.Equal(t, 1, fake.base.writes)
	assert.Equal(t, p.armTemplate, fake.base.template)
	assert.Empty(t, compiler.buildParamCalls, "the customized compiled plan has no resource-token expression")
	assert.Equal(t, map[string]any{"value": offlineKubeletObjectID}, fake.base.parameters["kubeletObjectId"])
}

func TestBYOSuppliedIdentityRequiresRealPrincipalBinding(t *testing.T) {
	for _, body := range []map[string]any{
		{"id": suppliedHostingID},
		{"id": suppliedHostingID, "properties": map[string]any{"principalId": "invalid"}},
		{"id": suppliedWorkloadID, "properties": map[string]any{"principalId": suppliedHostingPrincipal}},
	} {
		p, fake, _ := suppliedProvider(t, 2)
		fake.overrides[strings.ToLower(suppliedHostingID)] = suppliedReply{status: 200, body: body}
		_, err := p.Deploy(t.Context(), func(string) {})
		require.Error(t, err)
		assert.Zero(t, fake.base.writes)
		assert.Zero(t, fake.base.previews)
	}
}
