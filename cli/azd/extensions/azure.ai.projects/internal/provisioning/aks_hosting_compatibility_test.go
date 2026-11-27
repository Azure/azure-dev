// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"fmt"
	"maps"
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

func customBYOIdentityPlanTemplate(
	hostingID, workloadID, storageID, hostingPrincipal, workloadPrincipal string,
) map[string]any {
	return map[string]any{
		"$schema":         "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"languageVersion": "2.0",
		"contentVersion":  "1.0.0.0",
		"parameters": map[string]any{
			"agentHosting": map[string]any{
				"type": "object",
				"defaultValue": map[string]any{
					"enabled": false, "clusterResourceId": "", "storageAccountResourceId": "",
					"hostingManagementIdentityResourceId": "", "workloadIdentityResourceId": "",
				},
			},
			"includeAcr":                        map[string]any{"type": "bool", "defaultValue": false},
			"kubeletObjectId":                   map[string]any{"type": "string", "defaultValue": ""},
			"federatedIdentityRoleDefinitionId": map[string]any{"type": "string", "defaultValue": ""},
			"byoAksExistingRoleAssignmentKeys":  map[string]any{"type": "array", "defaultValue": []any{}},
			"hostingPrincipalId":                map[string]any{"type": "string", "defaultValue": ""},
			"workloadPrincipalId":               map[string]any{"type": "string", "defaultValue": ""},
			"monitoringMode":                    map[string]any{"type": "string", "defaultValue": "none"},
		},
		"resources": map[string]any{
			"customDeployment": map[string]any{
				"type": "Microsoft.Resources/deployments", "apiVersion": "2025-04-01",
				"name": "custom-deployment", "resourceGroup": "target-rg",
				"properties": map[string]any{
					"expressionEvaluationOptions": map[string]any{"scope": "inner"},
					"parameters": map[string]any{
						"forwardedHostingPrincipal":  map[string]any{"value": "[parameters('hostingPrincipalId')]"},
						"forwardedWorkloadPrincipal": map[string]any{"value": "[parameters('workloadPrincipalId')]"},
					},
					"template": map[string]any{
						"$schema":         "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
						"languageVersion": "2.0",
						"parameters": map[string]any{
							"forwardedHostingPrincipal":  map[string]any{"type": "string", "defaultValue": ""},
							"forwardedWorkloadPrincipal": map[string]any{"type": "string", "defaultValue": ""},
						},
						"resources": map[string]any{
							"customHosting": map[string]any{
								"type": "Microsoft.ManagedIdentity/userAssignedIdentities", "apiVersion": "2023-01-31",
								"name": hostingID[strings.LastIndex(hostingID, "/")+1:], "location": "westus",
							},
							"customWorkload": map[string]any{
								"type": "Microsoft.ManagedIdentity/userAssignedIdentities", "apiVersion": "2023-01-31",
								"name": workloadID[strings.LastIndex(workloadID, "/")+1:], "location": "westus",
							},
							"customStorage": map[string]any{
								"type": "Microsoft.Storage/storageAccounts", "apiVersion": "2023-05-01",
								"name": storageID[strings.LastIndex(storageID, "/")+1:], "location": "westus",
							},
							"customAccount": map[string]any{
								"type": "Microsoft.CognitiveServices/accounts", "apiVersion": "2025-04-01-preview",
								"name": "custom-account",
								"properties": map[string]any{
									"agentHostingConfigurations": []any{map[string]any{
										"hostingType":                         "ManagedCluster",
										"clusterResourceId":                   offlineClusterID,
										"storageAccountResourceId":            storageID,
										"hostingManagementIdentityResourceId": hostingID,
										"workloadIdentityResourceId":          workloadID,
									}},
								},
							},
							"hostingGrant": map[string]any{
								"type": "Microsoft.Authorization/roleAssignments", "apiVersion": "2022-04-01",
								"name":       "[guid(resourceGroup().id, 'hosting-grant')]",
								"properties": map[string]any{"principalId": hostingPrincipal},
							},
							"workloadGrant": map[string]any{
								"type": "Microsoft.Authorization/roleAssignments", "apiVersion": "2022-04-01",
								"name":       "[guid(resourceGroup().id, 'workload-grant')]",
								"properties": map[string]any{"principalId": workloadPrincipal},
							},
						},
					},
				},
			},
		},
		"outputs": map[string]any{
			"AZURE_AI_PROJECT_ID":                  map[string]any{"type": "string", "value": offlineProjectID},
			"AZURE_AI_PROJECT_ENDPOINT":            map[string]any{"type": "string", "value": offlineProjectEndpoint},
			"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"type": "string", "value": ""},
		},
	}
}

func TestBYOAutomaticPrincipalBindingsFollowCompiledPlan(t *testing.T) {
	const uamiPrefix = suppliedGroupID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/"
	const existingHostingID = uamiPrefix + "existing-hosting"
	const existingWorkloadID = uamiPrefix + "existing-workload"
	const createdHostingID = uamiPrefix + "created-hosting"
	const createdWorkloadID = uamiPrefix + "created-workload"
	const storageID = suppliedGroupID + "/providers/Microsoft.Storage/storageAccounts/storage"

	tests := []struct {
		name               string
		selectorMask       int
		hostingID          string
		workloadID         string
		hostingPrincipal   string
		workloadPrincipal  string
		existingIdentities bool
		wantErr            string
	}{
		{
			name:               "emptySelectorUpdatesExistingIdentity",
			selectorMask:       0,
			hostingID:          existingHostingID,
			workloadID:         existingWorkloadID,
			hostingPrincipal:   "[parameters('forwardedHostingPrincipal')]",
			workloadPrincipal:  "[parameters('forwardedWorkloadPrincipal')]",
			existingIdentities: true,
		},
		{
			name:              "nonemptySelectorIgnoredByNewIdentity",
			selectorMask:      6,
			hostingID:         createdHostingID,
			workloadID:        createdWorkloadID,
			hostingPrincipal:  "[reference('customHosting').principalId]",
			workloadPrincipal: "[reference('customWorkload').principalId]",
		},
		{
			name:              "missingConsumedIdentityBlocksPreflight",
			selectorMask:      0,
			hostingID:         createdHostingID,
			workloadID:        createdWorkloadID,
			hostingPrincipal:  "[parameters('forwardedHostingPrincipal')]",
			workloadPrincipal: "[reference('customWorkload').principalId]",
			wantErr:           "Hosting managed identity does not meet requirements.",
		},
	}

	for _, tt := range tests {
		for _, preview := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/preview=%t", tt.name, preview), func(t *testing.T) {
				p, fake, compiler := suppliedProvider(t, tt.selectorMask)
				if tt.existingIdentities {
					fake.missingGrant = offlineClusterID
					for _, item := range []struct{ id, principal string }{
						{tt.hostingID, suppliedHostingPrincipal},
						{tt.workloadID, suppliedWorkloadPrincipal},
					} {
						fake.overrides[strings.ToLower(item.id)] = suppliedReply{
							status: 200,
							body: map[string]any{
								"id": item.id,
								"properties": map[string]any{
									"principalId": item.principal, "clientId": item.principal, "tenantId": "tenant",
								},
							},
						}
					}
				}
				template := customBYOIdentityPlanTemplate(
					tt.hostingID, tt.workloadID, storageID, tt.hostingPrincipal, tt.workloadPrincipal,
				)
				p.armTemplate = template
				p.onDiskSource = &templateSource{
					mode:        templateModeBicep,
					armTemplate: template,
					parameters:  map[string]any{"monitoringMode": map[string]any{"value": "none"}},
				}

				var err error
				if preview {
					_, err = p.Preview(t.Context(), func(string) {})
				} else {
					_, err = p.Deploy(t.Context(), func(string) {})
				}
				assert.Empty(t, compiler.buildParamCalls)
				if tt.wantErr != "" {
					require.ErrorContains(t, err, tt.wantErr)
					require.ErrorContains(t, err, "Managed identity does not exist")
					assert.Contains(t, fake.requests, "GET "+tt.hostingID)
					assert.NotContains(t, fake.requests, "GET "+tt.workloadID)
					assert.Zero(t, fake.base.writes)
					assert.Zero(t, fake.base.previews)
					return
				}
				require.NoError(t, err)
				if preview {
					assert.Equal(t, 1, fake.base.previews)
					assert.Zero(t, fake.base.writes)
				} else {
					assert.Equal(t, 1, fake.base.writes)
					assert.Zero(t, fake.base.previews)
				}
				if tt.existingIdentities {
					assert.Contains(t, fake.requests, "GET "+tt.hostingID)
					assert.Contains(t, fake.requests, "GET "+tt.workloadID)
					assert.Equal(t, map[string]any{"value": suppliedHostingPrincipal},
						fake.base.parameters["hostingPrincipalId"])
					assert.Equal(t, map[string]any{"value": suppliedWorkloadPrincipal},
						fake.base.parameters["workloadPrincipalId"])
				} else {
					assert.NotContains(t, fake.requests, "GET "+tt.hostingID)
					assert.NotContains(t, fake.requests, "GET "+tt.workloadID)
					assert.NotContains(t, fake.requests, "GET "+suppliedHostingID)
					assert.NotContains(t, fake.requests, "GET "+suppliedWorkloadID)
					assert.NotContains(t, fake.base.parameters, "hostingPrincipalId")
					assert.NotContains(t, fake.base.parameters, "workloadPrincipalId")
				}
			})
		}
	}
}
