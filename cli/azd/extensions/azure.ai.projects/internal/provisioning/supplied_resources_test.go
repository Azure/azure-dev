// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const suppliedStorageID = "/subscriptions/data-sub/resourceGroups/data-rg/providers/Microsoft.Storage/storageAccounts/data"
const suppliedHostingID = "/subscriptions/identity-sub/resourceGroups/identity-rg/providers/" +
	"Microsoft.ManagedIdentity/userAssignedIdentities/hosting"
const suppliedWorkloadID = "/subscriptions/workload-sub/resourceGroups/workload-rg/providers/" +
	"Microsoft.ManagedIdentity/userAssignedIdentities/workload"
const suppliedHostingPrincipal = "22222222-2222-4222-8222-222222222222"
const suppliedWorkloadPrincipal = "33333333-3333-4333-8333-333333333333"

type suppliedARMFake struct {
	base          *byoARMFake
	reads         []string
	identityReply map[string]any
	replies       map[string]any
}

func (f *suppliedARMFake) Do(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet {
		f.reads = append(f.reads, request.URL.Path)
		if reply, ok := f.replies[request.URL.Path]; ok {
			return f.base.response(request, 200, reply), nil
		}
		for _, identity := range []struct{ id, principal string }{
			{suppliedHostingID, suppliedHostingPrincipal},
			{suppliedWorkloadID, suppliedWorkloadPrincipal},
		} {
			if strings.EqualFold(request.URL.Path, identity.id) {
				body := f.identityReply
				if body == nil {
					body = map[string]any{"id": identity.id, "properties": map[string]any{
						"principalId": identity.principal,
					}}
				}
				return f.base.response(request, 200, body), nil
			}
		}
		if request.URL.Path == "/subscriptions/workload-sub/providers/Microsoft.Authorization/roleDefinitions" {
			return f.base.response(request, 200, map[string]any{"value": []any{map[string]any{
				"id": request.URL.Path + "/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
				"properties": map[string]any{
					"roleName": "Managed Identity Federated Identity Credential Contributor", "type": "BuiltInRole",
				},
			}}}), nil
		}
	}
	return f.base.Do(request)
}

func suppliedYAML(mask int) string {
	var raw strings.Builder
	raw.WriteString("services:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n    agentHosting:\n" +
		"      clusterResourceId: " + offlineClusterID + "\n")
	for i, selection := range []struct{ name, id string }{
		{"storageAccountResourceId", suppliedStorageID},
		{"hostingManagementIdentityResourceId", suppliedHostingID},
		{"workloadIdentityResourceId", suppliedWorkloadID},
	} {
		if mask&(1<<i) != 0 {
			raw.WriteString("      " + selection.name + ": " + selection.id + "\n")
		}
	}
	return raw.String()
}

func suppliedProvider(t *testing.T, mask int) (*FoundryProvisioningProvider, *suppliedARMFake) {
	t.Helper()
	p, base := offlineBYOProvider(t)
	result, err := synthesis.Synthesize(synthesis.Input{ServiceName: "project", RawAzureYAML: []byte(suppliedYAML(mask))})
	require.NoError(t, err)
	p.synthResult = result
	fake := &suppliedARMFake{base: base}
	p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: fake, Retry: policy.RetryOptions{MaxRetries: -1},
	}}
	return p, fake
}

func TestBYOSuppliedResourcesDeployIndependently(t *testing.T) {
	for mask := range 8 {
		for _, registry := range []bool{false, true} {
			for _, preview := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/registry=%t/preview=%t", mask, registry, preview), func(t *testing.T) {
					p, fake := suppliedProvider(t, mask)
					p.armTemplate["contentVersion"] = "2.0.0.0"
					resources, ok := p.armTemplate["resources"].(map[string]any)
					require.True(t, ok)
					resourceGroup, ok := resources["resourceGroup"].(map[string]any)
					require.True(t, ok)
					resourceGroup["name"] = "custom-resource-group"
					compiler := &stubCompiler{}
					p.bicepCliInstance = compiler
					p.onDiskSource = &templateSource{
						mode: templateModeBicep, armTemplate: p.armTemplate,
						parameters: map[string]any{
							"includeAcr":     map[string]any{"value": registry},
							"monitoringMode": map[string]any{"value": "none"},
						},
					}
					if preview {
						result, err := p.Preview(t.Context(), func(string) {})
						require.NoError(t, err)
						require.NotNil(t, result.Preview)
						assert.Zero(t, fake.base.writes)
						assert.Equal(t, 1, fake.base.previews)
					} else {
						result, err := p.Deploy(t.Context(), func(string) {})
						require.NoError(t, err)
						require.NotNil(t, result.Deployment)
						expected := ""
						if registry {
							expected = offlineRegistryID
						}
						assert.Equal(t, expected, result.Deployment.Outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"].Value)
						assert.Equal(t, 1, fake.base.writes)
					}
					envelope, ok := fake.base.parameters["agentHosting"].(map[string]any)
					require.True(t, ok)
					raw, err := json.Marshal(envelope["value"])
					require.NoError(t, err)
					var hosting map[string]any
					require.NoError(t, json.Unmarshal(raw, &hosting))
					for i, selection := range []struct{ name, id string }{
						{"storageAccountResourceId", suppliedStorageID},
						{"hostingManagementIdentityResourceId", suppliedHostingID},
						{"workloadIdentityResourceId", suppliedWorkloadID},
					} {
						expected := ""
						if mask&(1<<i) != 0 {
							expected = selection.id
						}
						assert.Equal(t, expected, hosting[selection.name])
					}
					for _, identity := range []struct {
						id, binding, principal string
						selected               bool
					}{
						{suppliedHostingID, "hostingPrincipalId", suppliedHostingPrincipal, mask&2 != 0},
						{suppliedWorkloadID, "workloadPrincipalId", suppliedWorkloadPrincipal, mask&4 != 0},
					} {
						if identity.selected {
							assert.Equal(t, map[string]any{"value": identity.principal},
								fake.base.parameters[identity.binding])
							assert.Contains(t, fake.reads, identity.id)
						} else {
							assert.NotContains(t, fake.base.parameters, identity.binding)
							assert.NotContains(t, fake.reads, identity.id)
						}
					}
					roleSubscription := "target-sub"
					if mask&4 != 0 {
						roleSubscription = "workload-sub"
					}
					assert.Equal(t, map[string]any{"value": "/subscriptions/" + roleSubscription +
						"/providers/Microsoft.Authorization/roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
						fake.base.parameters["federatedIdentityRoleDefinitionId"])
					assert.Contains(t, fake.reads, offlineClusterID)
					if registry {
						assert.Contains(t, fake.base.parameters, "kubeletObjectId")
					} else {
						assert.NotContains(t, fake.base.parameters, "kubeletObjectId")
					}
					assert.Empty(t, compiler.buildCalls)
					assert.Empty(t, compiler.buildParamCalls)
					assert.Empty(t, p.virtualEnv)
					assert.Equal(t, p.armTemplate, fake.base.template)
				})
			}
		}
	}
}

func TestBYOSuppliedBindingsPreserveExplicitValues(t *testing.T) {
	for _, binding := range []string{"hostingPrincipalId", "workloadPrincipalId", "federatedIdentityRoleDefinitionId"} {
		mask := 0
		switch binding {
		case "hostingPrincipalId":
			mask = 2
		case "workloadPrincipalId":
			mask = 4
		}
		for _, declared := range []bool{false, true} {
			for _, preview := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/declared=%t/preview=%t", binding, declared, preview), func(t *testing.T) {
					p, fake := suppliedProvider(t, mask)
					source, err := p.resolveTemplate(t.Context(), func(string) {})
					require.NoError(t, err)
					explicit := map[string]any{"value": "explicit-" + binding}
					parameterName := strings.ToUpper(binding)
					source.parameters[parameterName] = explicit
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
					assert.Equal(t, explicit, fake.base.parameters[parameterName])
					assert.NotContains(t, fake.base.parameters, binding)
					assert.Contains(t, fake.reads, offlineClusterID)
					switch binding {
					case "hostingPrincipalId":
						assert.NotContains(t, fake.reads, suppliedHostingID)
					case "workloadPrincipalId":
						assert.NotContains(t, fake.reads, suppliedWorkloadID)
					case "federatedIdentityRoleDefinitionId":
						assert.NotContains(t, fake.reads,
							"/subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions")
					}
				})
			}
		}
	}
}

func TestBYOSuppliedUsesEffectiveDeploymentInputs(t *testing.T) {
	for _, name := range []string{
		"parameters", "template default", "without YAML", "disabled", "undeclared", "explicit bindings",
	} {
		t.Run(name, func(t *testing.T) {
			p, fake := suppliedProvider(t, 7)
			clusterID := offlineClusterID + "-effective"
			hostingID := suppliedHostingID + "-effective"
			workloadID := strings.Replace(suppliedWorkloadID, "workload-sub", "effective-sub", 1) + "-effective"
			hosting := map[string]any{
				"enabled": true, "hostingType": "custom", "name": "custom",
				"clusterResourceId": clusterID, "storageAccountResourceId": suppliedStorageID + "-effective",
				"hostingManagementIdentityResourceId": hostingID, "workloadIdentityResourceId": workloadID,
				"customSetting": true,
			}
			parameters := map[string]any{
				"AGENTHOSTING":      map[string]any{"value": hosting},
				"includeAcr":        map[string]any{"value": true},
				"location":          map[string]any{"value": "[parameters('customRegion')]"},
				"resourceTokenSalt": map[string]any{"value": nil},
				"resourceGroupName": map[string]any{"value": false},
			}
			definitions, ok := p.armTemplate["parameters"].(map[string]any)
			require.True(t, ok)
			switch name {
			case "template default", "without YAML":
				result, err := synthesis.Synthesize(synthesis.Input{ServiceName: "project",
					RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n")})
				require.NoError(t, err)
				p.synthResult = result
				if name == "template default" {
					delete(parameters, "AGENTHOSTING")
					definition, ok := definitions["agentHosting"].(map[string]any)
					require.True(t, ok)
					definition["defaultValue"] = hosting
				}
			case "disabled":
				hosting["enabled"] = false
				hosting["clusterResourceId"] = 42
			case "undeclared":
				delete(definitions, "agentHosting")
				parameters["AGENTHOSTING"] = map[string]any{"value": "leave undeclared parameters to ARM"}
			case "explicit bindings":
				hosting["hostingManagementIdentityResourceId"] = map[string]any{"custom": true}
				hosting["workloadIdentityResourceId"] = 42
				for _, binding := range []string{
					"hostingPrincipalId", "workloadPrincipalId", "federatedIdentityRoleDefinitionId", "kubeletObjectId",
				} {
					parameters[binding] = map[string]any{"value": "explicit-" + binding}
				}
			}
			parameters["monitoringMode"] = map[string]any{"value": "none"}
			p.onDiskSource = &templateSource{
				mode: templateModeBicep, armTemplate: p.armTemplate, parameters: parameters,
			}
			rolePath := "/subscriptions/effective-sub/providers/Microsoft.Authorization/roleDefinitions"
			fake.replies = map[string]any{
				clusterID: map[string]any{"properties": fake.base.clusterProperties},
				hostingID: map[string]any{
					"id": hostingID, "properties": map[string]any{"principalId": suppliedHostingPrincipal},
				},
				workloadID: map[string]any{
					"id": workloadID, "properties": map[string]any{"principalId": suppliedWorkloadPrincipal},
				},
				rolePath: map[string]any{"value": []any{map[string]any{
					"id": rolePath + "/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
					"properties": map[string]any{
						"roleName": "Managed Identity Federated Identity Credential Contributor", "type": "BuiltInRole",
					},
				}}},
			}
			source, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			require.NoError(t, p.prepareBYO(t.Context(), source))
			for parameter, value := range parameters {
				assert.Equal(t, value, source.parameters[parameter])
			}
			assert.NotContains(t, source.parameters, "agentHosting", "the original object or template default is preserved")
			switch name {
			case "disabled", "undeclared":
				assert.Empty(t, fake.reads)
				assert.NotContains(t, source.parameters, "kubeletObjectId")
				assert.NotContains(t, source.parameters, "federatedIdentityRoleDefinitionId")
			case "explicit bindings":
				assert.Equal(t, []string{clusterID}, fake.reads)
			default:
				assert.ElementsMatch(t, []string{clusterID, rolePath, hostingID, workloadID}, fake.reads)
				assert.Equal(t, map[string]any{"value": suppliedHostingPrincipal}, source.parameters["hostingPrincipalId"])
				assert.Equal(t, map[string]any{"value": suppliedWorkloadPrincipal}, source.parameters["workloadPrincipalId"])
				assert.Equal(t, map[string]any{"value": offlineKubeletObjectID}, source.parameters["kubeletObjectId"])
				assert.Equal(t, map[string]any{"value": rolePath + "/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
					source.parameters["federatedIdentityRoleDefinitionId"])
			}
			assert.Empty(t, p.virtualEnv)
		})
	}
}

func TestBYOSuppliedIdentityRequiresRealPrincipalBinding(t *testing.T) {
	for _, body := range []map[string]any{
		{"id": suppliedHostingID},
		{"id": suppliedHostingID, "properties": map[string]any{"principalId": "invalid"}},
		{"id": suppliedWorkloadID, "properties": map[string]any{"principalId": suppliedHostingPrincipal}},
	} {
		p, fake := suppliedProvider(t, 2)
		fake.identityReply = body
		_, err := p.Deploy(t.Context(), func(string) {})
		require.Error(t, err)
		assert.Zero(t, fake.base.writes)
		assert.Zero(t, fake.base.previews)
	}
}
