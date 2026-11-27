// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
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
					parameters: map[string]any{
						"includeAcr":     map[string]any{"value": value},
						"monitoringMode": map[string]any{"value": "none"},
					},
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

func TestBYOPrepareValidatesSuppliedKubeletForACR(t *testing.T) {
	for _, includeAcr := range []bool{false, true} {
		for _, test := range []struct {
			name      string
			parameter any
			duplicate bool
			valid     bool
		}{
			{"empty", map[string]any{"value": ""}, false, false},
			{"whitespace", map[string]any{"value": " "}, false, false},
			{"non-string", map[string]any{"value": 1}, false, false},
			{"invalid GUID", map[string]any{"value": "not-a-guid"}, false, false},
			{"null", map[string]any{"value": nil}, false, false},
			{"reference", map[string]any{"reference": map[string]any{"secretName": "kubelet"}}, false, false},
			{"extra envelope field", map[string]any{"value": offlineKubeletObjectID, "extra": true}, false, false},
			{"duplicate", map[string]any{"value": offlineKubeletObjectID}, true, false},
			{"different", map[string]any{"value": suppliedHostingPrincipal}, false, false},
			{"matching", map[string]any{"value": strings.ToUpper(offlineKubeletObjectID)}, false, true},
		} {
			t.Run(fmt.Sprintf("includeAcr=%t/%s", includeAcr, test.name), func(t *testing.T) {
				p, fake := offlineBYOProvider(t)
				source, err := p.resolveTemplate(t.Context(), func(string) {})
				require.NoError(t, err)
				delete(source.parameters, "includeAcr")
				source.parameters["INCLUDEACR"] = map[string]any{"value": includeAcr}
				source.parameters["KUBELETOBJECTID"] = test.parameter
				if test.duplicate {
					source.parameters["kubeletObjectId"] = test.parameter
				}
				err = p.prepareBYO(t.Context(), source, func(string) {})
				if includeAcr && !test.valid {
					require.ErrorContains(t, err, "kubeletObjectId")
					if test.name == "different" {
						require.ErrorContains(t, err, suppliedHostingPrincipal)
						require.ErrorContains(t, err, offlineKubeletObjectID)
						require.ErrorContains(t, err, offlineClusterID)
						assert.Contains(t, fake.requests, "GET "+offlineClusterID)
					} else {
						assert.Empty(t, fake.requests, "invalid local inputs must fail before Azure reads")
					}
				} else {
					require.NoError(t, err)
					assert.Contains(t, fake.requests, "GET "+offlineClusterID)
				}
				assert.Equal(t, test.parameter, source.parameters["KUBELETOBJECTID"])
				if test.duplicate {
					assert.Equal(t, test.parameter, source.parameters["kubeletObjectId"])
				}
				assert.Equal(t, map[string]any{"value": includeAcr}, source.parameters["INCLUDEACR"])
			})
		}
	}
}

func TestBYOKubeletBindingFailureBeforeRegistryWrites(t *testing.T) {
	for _, preview := range []bool{false, true} {
		for _, external := range []bool{false, true} {
			for _, failure := range []string{"mismatch", "invalid GUID", "missing actual identity"} {
				t.Run(fmt.Sprintf("preview=%t/external=%t/%s", preview, external, failure), func(t *testing.T) {
					t.Setenv(synthesis.ACRReuseEnvVar, "true")
					p, fake := offlineBYOProvider(t)
					env := &resolveEnvStubEnvServer{envName: "test"}
					p.azdClient = newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
					p.acrPromptedEnv = map[string]string{envKeySubscriptionID: p.subID, envKeyLocation: p.location}
					source, err := p.resolveTemplate(t.Context(), func(string) {})
					require.NoError(t, err)
					source.parameters["includeAcr"] = map[string]any{"value": true}
					principal := suppliedHostingPrincipal
					expectedError := "does not match"
					switch failure {
					case "invalid GUID":
						principal = "not-a-guid"
						expectedError = "literal principal object ID GUID"
					case "missing actual identity":
						principal = offlineKubeletObjectID
						fake.kubeletID = ""
						expectedError = "Kubelet identity principal could not be verified"
					}
					source.parameters["kubeletObjectId"] = map[string]any{"value": principal}
					if external {
						source.parameters["existingAcrResourceId"] = map[string]any{
							"value": "/subscriptions/target-sub/resourceGroups/shared/providers/" +
								"Microsoft.ContainerRegistry/registries/registry",
						}
					}
					original := maps.Clone(source.parameters)
					p.onDiskSource = source
					p.acrRequest = func(_ context.Context, method, path, _ string, _ any) error {
						t.Errorf("registry access must not start after invalid kubelet binding: %s %s", method, path)
						return fmt.Errorf("unexpected registry request")
					}
					if preview {
						_, err = p.Preview(t.Context(), func(string) {})
					} else {
						_, err = p.Deploy(t.Context(), func(string) {})
					}
					require.ErrorContains(t, err, expectedError)
					assert.Equal(t, original, source.parameters)
					assert.Zero(t, fake.writes)
					assert.Zero(t, fake.previews)
					assert.Empty(t, env.set)
					assert.Empty(t, env.config)
					assert.Nil(t, p.acrPending)
					assert.Len(t, p.acrPromptedEnv, 2)
				})
			}
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
				source.armTemplate["resources"] = map[string]any{"account": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "cog-native",
					"properties": map[string]any{"allowProjectManagement": true},
				}}
				source.parameters["AGENTHOSTING"] = map[string]any{"value": "leave undeclared parameters to ARM"}
			}
			var progress []string
			require.NoError(t, p.prepareBYO(t.Context(), source, func(message string) {
				progress = append(progress, message)
			}))
			assert.NotContains(t, source.parameters, "agentHosting")
			if name == "disabled" || name == "undeclared" {
				assert.Empty(t, fake.requests)
				assert.NotContains(t, source.parameters, "federatedIdentityRoleDefinitionId")
				assert.Empty(t, progress)
			} else {
				assert.Equal(t, []string{
					"Checking AKS and supporting resources...",
					"Checking required permissions...",
					"Resolving deployment identity and role bindings...",
				}, byoPreparationStages(progress))
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
		parameters: map[string]any{
			"includeAcr":     map[string]any{"value": true},
			"monitoringMode": map[string]any{"value": "none"},
		},
	}
	source, err := p.resolveTemplate(t.Context(), func(string) {})
	require.NoError(t, err)
	plan, err := p.identifyBYOTemplatePlan(t.Context(), source, nil)
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

func customBYOProperties(clusterExpr any) map[string]any {
	return map[string]any{"agentHostingConfigurations": []any{map[string]any{
		"hostingType":                         "ManagedCluster",
		"clusterResourceId":                   clusterExpr,
		"storageAccountResourceId":            suppliedStorageID,
		"hostingManagementIdentityResourceId": suppliedHostingID,
		"workloadIdentityResourceId":          suppliedWorkloadID,
	}}}
}

func customTemplateWithoutAgentHosting(
	t *testing.T, mask int, accountProps any,
) (*FoundryProvisioningProvider, *suppliedARMFake, *templateSource) {
	t.Helper()
	p, fake, _ := suppliedProvider(t, mask)
	result, err := synthesis.Synthesize(synthesis.Input{
		ServiceName:  "project",
		RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n"),
	})
	require.NoError(t, err)
	p.synthResult = result
	definitions, ok := p.armTemplate["parameters"].(map[string]any)
	require.True(t, ok)
	delete(definitions, "agentHosting")
	delete(definitions, "kubeletObjectId")
	definitions["aksClusterId"] = map[string]any{"type": "string", "defaultValue": offlineClusterID}
	p.armTemplate["resources"] = map[string]any{"customAccount": map[string]any{
		"type": "Microsoft.CognitiveServices/accounts", "name": "custom-account", "properties": accountProps,
	}}
	source := &templateSource{
		mode: templateModeBicep, armTemplate: p.armTemplate,
		parameters: map[string]any{
			"includeAcr":     map[string]any{"value": false},
			"monitoringMode": map[string]any{"value": "none"},
		},
	}
	p.onDiskSource = source
	return p, fake, source
}

func TestBYOCustomTemplateWithoutAgentHosting(t *testing.T) {
	customClusterID := offlineClusterID + "-custom"
	for _, preview := range []bool{false, true} {
		for _, scenario := range []string{
			"renamed parameter", "property loop", "resource loop", "union properties", "union preflight failure",
			"disabled agentHosting parameter", "preflight failure",
		} {
			t.Run(fmt.Sprintf("preview=%t/%s", preview, scenario), func(t *testing.T) {
				targetClusterID := customClusterID
				props := any(customBYOProperties("[parameters('aksClusterId')]"))
				if scenario == "property loop" {
					targetClusterID = customClusterID + "-0"
					props = map[string]any{"copy": []any{map[string]any{
						"name":  "agentHostingConfigurations",
						"count": "[length(createArray(parameters('aksClusterId')))]",
						"input": customBYOProperties(
							"[format('{0}-{1}', parameters('aksClusterId'), copyIndex('agentHostingConfigurations'))]",
						)["agentHostingConfigurations"].([]any)[0],
					}}}
				} else if scenario == "resource loop" {
					props = customBYOProperties("[createArray('unused', parameters('aksClusterId'))[copyIndex('accounts')]]")
				} else if strings.HasPrefix(scenario, "union ") {
					props = "[union(variables('baseProps'), variables('aksProps'))]"
				}
				p, fake, source := customTemplateWithoutAgentHosting(t, 7, props)
				if scenario == "resource loop" {
					acc := p.armTemplate["resources"].(map[string]any)["customAccount"].(map[string]any)
					acc["copy"] = map[string]any{"name": "accounts", "count": 2.0}
					acc["condition"] = "[equals(copyIndex('accounts'), 1)]"
				}
				p.armTemplate["variables"] = map[string]any{
					"baseProps": map[string]any{"allowProjectManagement": true},
					"aksProps":  customBYOProperties("[parameters('aksClusterId')]"),
				}
				fake.base.clusterID = targetClusterID
				source.parameters["aksClusterId"] = map[string]any{"value": customClusterID}
				if strings.HasSuffix(scenario, "preflight failure") {
					fake.base.clusterVersion = "1.30.0"
				}
				if scenario == "disabled agentHosting parameter" {
					p.armTemplate["parameters"].(map[string]any)["agentHosting"] = map[string]any{"type": "object"}
					source.parameters["agentHosting"] = map[string]any{"value": map[string]any{
						"enabled": false, "clusterResourceId": 42,
					}}
				}

				var err error
				if preview {
					_, err = p.Preview(t.Context(), func(string) {})
				} else {
					_, err = p.Deploy(t.Context(), func(string) {})
				}

				assert.Contains(t, fake.requests, "GET "+targetClusterID)
				if strings.HasSuffix(scenario, "preflight failure") {
					require.ErrorContains(t, err, "The running Kubernetes version is below the required minimum")
					assert.Zero(t, fake.base.writes)
					assert.Zero(t, fake.base.previews)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"value": suppliedHostingPrincipal},
					fake.base.parameters["hostingPrincipalId"])
				assert.Equal(t, map[string]any{"value": suppliedWorkloadPrincipal},
					fake.base.parameters["workloadPrincipalId"])
				assert.Contains(t, fake.base.parameters, "federatedIdentityRoleDefinitionId")
				if scenario != "disabled agentHosting parameter" {
					assert.NotContains(t, fake.base.parameters, "agentHosting")
				}
			})
		}
	}
}

func TestBYOCustomTemplateGateAndNonAKSIsolation(t *testing.T) {
	const registryID = "/subscriptions/target-sub/resourceGroups/shared/providers/" +
		"Microsoft.ContainerRegistry/registries/registry"
	t.Setenv(synthesis.ACRReuseEnvVar, "true")

	for _, props := range []any{
		customBYOProperties(offlineClusterID),
		map[string]any{"agentHostingConfigurations": "[reference('runtimeSource').configs]"},
		"[union(variables('baseProps'), variables('aksProps'))]",
	} {
		p, fake, source := customTemplateWithoutAgentHosting(t, 7, props)
		p.armTemplate["variables"] = map[string]any{
			"baseProps": map[string]any{"allowProjectManagement": true},
			"aksProps":  customBYOProperties(offlineClusterID),
		}
		p.acrRemoteBuild = true
		source.parameters["includeAcr"] = map[string]any{"value": true}
		source.parameters["existingAcrResourceId"] = map[string]any{"value": registryID}
		p.acrRequest = func(_ context.Context, method, path, _ string, _ any) error {
			t.Fatalf("remote-build ACR read must not precede BYO preview gate: %s %s", method, path)
			return nil
		}
		t.Setenv(synthesis.BYOAKSEnvVar, "false")
		p.credential = nil
		_, err := p.Deploy(t.Context(), func(string) {})
		requireBYOFeatureNotEnabled(t, err)
		assert.Nil(t, p.credential)
		assert.Empty(t, fake.requests)
	}

	p, fake, source := customTemplateWithoutAgentHosting(t, 7, customBYOProperties(offlineClusterID))
	source.parameters["existingAcrResourceId"] = map[string]any{"value": registryID}
	t.Setenv(synthesis.BYOAKSEnvVar, "true")
	p.credential = nil
	_, err := p.Deploy(t.Context(), func(string) {})
	require.ErrorContains(t, err, "requires agentHosting.enabled=true")
	assert.Nil(t, p.credential)
	assert.Empty(t, fake.requests)

	// Existing-project fallback BYO rejects acrMode create/reuse-connect (parameter or ejected output)
	// while allowing none/already-connected and preserving root parameter precedence.
	for _, ejectedOutput := range []bool{false, true} {
		for _, tc := range []struct {
			mode   string
			reject bool
		}{
			{"create", true},
			{"reuse-connect", true},
			{"none", false},
			{"already-connected", false},
		} {
			p, _, source = customTemplateWithoutAgentHosting(t, 7, customBYOProperties(offlineClusterID))
			p.brownfieldEndpoint = offlineProjectEndpoint
			if ejectedOutput {
				p.armTemplate["outputs"] = map[string]any{
					"AZD_FOUNDRY_ACR_MODE": map[string]any{"type": "string", "value": tc.mode},
				}
			} else {
				source.parameters["acrMode"] = map[string]any{"value": tc.mode}
				p.armTemplate["outputs"] = map[string]any{
					"AZD_FOUNDRY_ACR_MODE": map[string]any{"type": "string", "value": "create"},
				}
			}
			source.parameters["includeAcr"] = map[string]any{"value": true}
			source.parameters["existingAcrResourceId"] = map[string]any{"value": registryID}
			_, err = p.prepareBYOLocal(source)
			if tc.reject {
				require.ErrorContains(t, err, "acrMode create or reuse-connect")
			} else {
				require.NoError(t, err)
			}
		}
	}

	// Both Deploy and Preview recover previous external ACR bindings (including without root outputs) before submission.
	for _, preview := range []bool{false, true} {
		p, fake, _ = customTemplateWithoutAgentHosting(t, 7, customBYOProperties(offlineClusterID))
		env := &resolveEnvStubEnvServer{envName: "test"}
		p.azdClient = newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
		deployPath := "/subscriptions/target-sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
		opsPath := "/subscriptions/target-sub/resourceGroups/target-rg/" +
			"providers/Microsoft.Resources/deployments/foundry-resources/operations"
		p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
			require.Equal(t, http.MethodGet, method)
			switch path {
			case deployPath:
				return acrTestReply(out, map[string]any{"properties": map[string]any{"provisioningState": "Succeeded"}})
			case opsPath:
				return acrTestReply(out, map[string]any{"value": []any{map[string]any{"properties": map[string]any{
					"targetResource": map[string]any{
						"id":           offlineProjectID,
						"resourceType": "Microsoft.CognitiveServices/accounts/projects",
					},
				}}}})
			case offlineProjectID:
				return acrTestReply(out, map[string]any{
					"identity": map[string]string{"principalId": "22222222-2222-4222-8222-222222222222"},
				})
			case offlineProjectID + "/connections":
				return acrTestReply(out, map[string]any{"value": []any{map[string]any{"properties": map[string]any{
					"category": "ContainerRegistry",
					"metadata": map[string]string{"ResourceId": registryID},
				}}}})
			default:
				return acrTestReply(out, map[string]any{"value": []any{}})
			}
		}
		if preview {
			_, err = p.Preview(t.Context(), func(string) {})
		} else {
			_, err = p.Deploy(t.Context(), func(string) {})
		}
		require.ErrorContains(t, err, "requires agentHosting.enabled=true")
		assert.Zero(t, fake.base.writes)
		assert.Zero(t, fake.base.previews)
		assert.Empty(t, env.config)
	}

	p, fake, source = customTemplateWithoutAgentHosting(t, 7, customBYOProperties("/not-a-cluster"))
	t.Setenv(synthesis.BYOAKSEnvVar, "false")
	p.credential = nil
	require.ErrorContains(t, p.prepareBYO(t.Context(), source, func(string) {}),
		"expected a complete Microsoft.ContainerService/managedClusters resource ID")
	assert.Empty(t, fake.requests)

	// Reject multiple active BYO configurations before output-based selection or cluster skipping.
	p, fake, _ = customTemplateWithoutAgentHosting(t, 7, nil)
	p.armTemplate["outputs"] = map[string]any{
		"AZURE_AI_ACCOUNT_NAME": map[string]any{"type": "string", "value": "primary"},
	}
	p.armTemplate["resources"] = map[string]any{
		"primary": map[string]any{
			"type": "Microsoft.CognitiveServices/accounts", "name": "primary",
			"properties": customBYOProperties(offlineClusterID),
		},
		"secondary": map[string]any{
			"type": "Microsoft.CognitiveServices/accounts", "name": "secondary",
			"properties": customBYOProperties(offlineClusterID + "-2"),
		},
	}
	t.Setenv(synthesis.BYOAKSEnvVar, "false")
	p.credential = nil
	_, err = p.Deploy(t.Context(), func(string) {})
	requireBYOFeatureNotEnabled(t, err)
	t.Setenv(synthesis.BYOAKSEnvVar, "true")
	for _, preview := range []bool{false, true} {
		if preview {
			_, err = p.Preview(t.Context(), func(string) {})
		} else {
			_, err = p.Deploy(t.Context(), func(string) {})
		}
		require.ErrorContains(t, err, "multiple active agentHostingConfigurations")
	}
	assert.Empty(t, fake.requests)
	assert.Zero(t, fake.base.writes)
	assert.Zero(t, fake.base.previews)

	// Resolve existing-RG location conditions online before rejecting multiple candidates.
	for _, tc := range []struct {
		name, secondCond, wantErr string
	}{
		{name: "complementary location selects single active cluster",
			secondCond: "[not(equals(resourceGroup().location, 'westus'))]"},
		{name: "both conditions active online",
			secondCond: "[equals(resourceGroup().location, 'westus')]",
			wantErr:    "multiple active agentHostingConfigurations"},
		{name: "unresolved runtime condition online",
			secondCond: "[equals(reference('runtimeSource', '2023-05-01').tier, 'aks')]",
			wantErr:    "cannot verify a unique active agentHostingConfigurations target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, fake, _ = customTemplateWithoutAgentHosting(t, 7, nil)
			p.armTemplate["outputs"] = map[string]any{
				"AZURE_AI_ACCOUNT_NAME": map[string]any{"type": "string", "value": "west-cog"},
			}
			p.armTemplate["resources"] = map[string]any{"scoped": map[string]any{
				"type": "Microsoft.Resources/deployments", "name": "scoped", "resourceGroup": "target-rg",
				"properties": map[string]any{
					"expressionEvaluationOptions": map[string]any{"scope": "inner"},
					"template": map[string]any{"resources": map[string]any{
						"west": map[string]any{
							"type": "Microsoft.CognitiveServices/accounts", "name": "west-cog",
							"condition":  "[equals(resourceGroup().location, 'westus')]",
							"properties": customBYOProperties(offlineClusterID),
						},
						"other": map[string]any{
							"type": "Microsoft.CognitiveServices/accounts", "name": "other-cog",
							"condition":  tc.secondCond,
							"properties": customBYOProperties(offlineClusterID + "-2"),
						},
					}},
				},
			}}
			if tc.wantErr == "" {
				t.Setenv(synthesis.BYOAKSEnvVar, "false")
				savedCred := p.credential
				p.credential = nil
				_, err = p.Deploy(t.Context(), func(string) {})
				requireBYOFeatureNotEnabled(t, err)
				assert.Nil(t, p.credential)
				assert.Empty(t, fake.requests)
				p.credential = savedCred
			}
			t.Setenv(synthesis.BYOAKSEnvVar, "true")
			for _, preview := range []bool{false, true} {
				if preview {
					_, err = p.Preview(t.Context(), func(string) {})
				} else {
					_, err = p.Deploy(t.Context(), func(string) {})
				}
				if tc.wantErr != "" {
					require.ErrorContains(t, err, tc.wantErr)
					assert.Zero(t, fake.base.writes)
					assert.Zero(t, fake.base.previews)
				} else {
					require.NoError(t, err)
					assert.Contains(t, fake.requests, "GET "+offlineClusterID)
					assert.NotContains(t, fake.requests, "GET "+offlineClusterID+"-2")
				}
			}
		})
	}

	// All conditional BYO candidates excluded online skip cluster preflight and fallback ACR rejection,
	// even when an unrelated linked deployment leaves resolver.unresolved non-empty.
	for _, preview := range []bool{false, true} {
		p, fake, source = customTemplateWithoutAgentHosting(t, 7, nil)
		source.parameters["includeAcr"] = map[string]any{"value": false}
		p.armTemplate["resources"] = map[string]any{
			"linked": map[string]any{
				"type": "Microsoft.Resources/deployments", "name": "linked",
				"properties": map[string]any{"templateLink": map[string]any{"uri": "https://example.com/linked.json"}},
			},
			"scoped": map[string]any{
				"type": "Microsoft.Resources/deployments", "name": "scoped", "resourceGroup": "target-rg",
				"properties": map[string]any{
					"expressionEvaluationOptions": map[string]any{"scope": "inner"},
					"template": map[string]any{"resources": map[string]any{"eastOnly": map[string]any{
						"type": "Microsoft.CognitiveServices/accounts", "name": "east-cog",
						"condition":  "[equals(resourceGroup().location, 'eastus')]",
						"properties": customBYOProperties(offlineClusterID),
					}}},
				},
			},
		}
		t.Setenv(synthesis.BYOAKSEnvVar, "true")
		if preview {
			source.parameters["includeAcr"] = map[string]any{"value": true}
			_, err = p.Preview(t.Context(), func(string) {})
			require.NoError(t, err)
			assert.Equal(t, 1, fake.base.previews)
		} else {
			_, err = p.Deploy(t.Context(), func(string) {})
			require.NoError(t, err)
			assert.Equal(t, 1, fake.base.writes)
		}
		assert.NotContains(t, fake.requests, "GET "+offlineClusterID)
	}

	// Resource-group scope read failure must still block when candidateCount drops to zero online.
	p, fake, _ = customTemplateWithoutAgentHosting(t, 7, nil)
	fake.overrides["/subscriptions/target-sub/resourcegroups/target-rg"] = suppliedReply{
		status: http.StatusForbidden,
		body:   map[string]any{"error": map[string]any{"code": "AuthorizationFailed"}},
	}
	p.armTemplate["resources"] = map[string]any{
		"rg": map[string]any{"type": "Microsoft.Resources/resourceGroups", "name": "target-rg", "location": "westus"},
		"scoped": map[string]any{
			"type": "Microsoft.Resources/deployments", "name": "scoped", "resourceGroup": "target-rg",
			"properties": map[string]any{
				"expressionEvaluationOptions": map[string]any{"scope": "inner"},
				"template": map[string]any{
					"variables": map[string]any{"aksProps": customBYOProperties(offlineClusterID)},
					"resources": map[string]any{"acc": map[string]any{
						"type": "Microsoft.CognitiveServices/accounts", "name": "west-cog",
						"properties": "[if(equals(resourceGroup().location, 'westus'), " +
							"variables('aksProps'), createObject('allowProjectManagement', true()))]",
					}},
				},
			},
		},
	}
	_, err = p.Deploy(t.Context(), func(string) {})
	require.ErrorContains(t, err, "read actual template resource-group scope")
	assert.Zero(t, fake.base.writes)

	p, fake, source = customTemplateWithoutAgentHosting(t, 0, nil)
	t.Setenv(synthesis.BYOAKSEnvVar, "false")
	p.credential = nil
	p.armTemplate["resources"] = map[string]any{
		"inactiveBYO": map[string]any{
			"type": "Microsoft.CognitiveServices/accounts", "name": "aks-cog", "condition": false,
			"properties": customBYOProperties(offlineClusterID),
		},
		"activeStandard": map[string]any{
			"type": "Microsoft.CognitiveServices/accounts", "name": "cog",
			"properties": map[string]any{"allowProjectManagement": true},
		},
		"emptyConfigs": map[string]any{
			"type": "Microsoft.CognitiveServices/accounts", "name": "cog-empty",
			"properties": map[string]any{"agentHostingConfigurations": []any{}},
		},
		"zeroCopy": map[string]any{
			"type": "Microsoft.CognitiveServices/accounts", "name": "cog-zero-copy",
			"properties": map[string]any{"copy": []any{map[string]any{
				"name": "agentHostingConfigurations", "count": 0.0,
				"input": customBYOProperties(offlineClusterID)["agentHostingConfigurations"].([]any)[0],
			}}},
		},
	}
	require.NoError(t, p.prepareBYO(t.Context(), source, func(string) {}))
	assert.Nil(t, p.credential)
	assert.Empty(t, fake.requests)
}

func TestBYORejectsSynthesizedHostingWhenOnDiskTemplateLacksAgentHosting(t *testing.T) {
	for _, preview := range []bool{false, true} {
		t.Run(fmt.Sprintf("preview=%t", preview), func(t *testing.T) {
			p, fake := offlineBYOProvider(t)
			t.Setenv(synthesis.BYOAKSEnvVar, "false")
			env := &resolveEnvStubEnvServer{envName: "test"}
			p.azdClient = newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
			p.credential = nil
			p.acrPromptedEnv = map[string]string{envKeySubscriptionID: p.subID, envKeyLocation: p.location}

			infraDir := filepath.Join(p.projectPath, onDiskInfraDir)
			require.NoError(t, os.MkdirAll(infraDir, 0o750))
			file := onDiskBicepFile
			if preview {
				file = onDiskBicepParamFile
			}
			expectedPath := filepath.Join(infraDir, file)
			require.NoError(t, os.WriteFile(expectedPath, []byte("// non-AKS\n"), 0o600))
			p.bicepCliInstance = &stubCompiler{
				buildResult: bicep.BuildResult{Compiled: minimalARMTemplate()},
				buildParamResult: bicep.BuildResult{
					Compiled: fmt.Sprintf(
						`{"templateJson":%q,"parametersJson":"{\"parameters\":{}}"}`, minimalARMTemplate(),
					),
				},
			}

			var err error
			if preview {
				_, err = p.Preview(t.Context(), func(string) {})
			} else {
				_, err = p.Deploy(t.Context(), func(string) {})
			}
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected *azdext.LocalError, got %T: %v", err, err)
			assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
			assert.Equal(t, exterrors.CodeInvalidServiceConfig, localErr.Code)
			assert.Contains(t, localErr.Message, expectedPath)
			assert.Contains(t, localErr.Suggestion, "regenerate or update")
			assert.Nil(t, p.credential)
			assert.Empty(t, fake.requests)
			assert.Empty(t, env.set)
		})
	}
}
