// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const offlineClusterID = "/subscriptions/cluster-sub/resourceGroups/aks-rg/providers/" +
	"Microsoft.ContainerService/managedClusters/aks"
const offlineKubeletObjectID = "11111111-aaaa-4111-8111-111111111111"
const offlineProjectID = "/subscriptions/target-sub/resourceGroups/target-rg/providers/" +
	"Microsoft.CognitiveServices/accounts/cog-native/projects/project"
const offlineProjectEndpoint = "https://cog-native.services.ai.azure.com/api/projects/project"
const offlineRegistryID = "/subscriptions/target-sub/resourceGroups/target-rg/providers/" +
	"Microsoft.ContainerRegistry/registries/crnative"
const offlineBYOAzureYAML = `
services:
  project:
    host: azure.ai.project
    monitoring: {enabled: false}
    agentHosting: {clusterResourceId: ` + offlineClusterID + `}
`

type byoTestCredential struct{}

func (byoTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "offline-only", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type byoARMFake struct {
	t                 *testing.T
	provider          *FoundryProvisioningProvider
	requests          []string
	parameters        map[string]any
	template          map[string]any
	clusterProperties map[string]any
	writes            int
	previews          int
	failWrite         bool
}

func (f *byoARMFake) response(request *http.Request, status int, body any) *http.Response {
	raw, err := json.Marshal(body)
	require.NoError(f.t, err)
	return &http.Response{StatusCode: status, Request: request, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(string(raw)))}
}

func (f *byoARMFake) Do(request *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, request.Method+" "+request.URL.Path)
	if request.Method == http.MethodGet && request.URL.Path == offlineClusterID {
		return f.response(request, 200, map[string]any{"properties": f.clusterProperties}), nil
	}
	if request.Method == http.MethodGet &&
		request.URL.Path == "/subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions" {
		return f.response(request, 200, map[string]any{"value": []any{map[string]any{
			"id": "/subscriptions/target-sub/providers/Microsoft.Authorization/" +
				"roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			"properties": map[string]any{"roleName": "Managed Identity Federated Identity Credential Contributor",
				"type": "BuiltInRole"},
		}}}), nil
	}
	path := "/subscriptions/target-sub/providers/Microsoft.Resources/deployments/" + f.provider.deploymentName()
	require.NotNil(f.t, request.Body, "unexpected ARM request: %s %s", request.Method, request.URL.Path)
	var submitted struct {
		Properties struct {
			Template   map[string]any `json:"template"`
			Parameters map[string]any `json:"parameters"`
		} `json:"properties"`
	}
	require.NoError(f.t, json.NewDecoder(request.Body).Decode(&submitted))
	f.template, f.parameters = submitted.Properties.Template, submitted.Properties.Parameters
	acrParameter, ok := f.parameters["includeAcr"].(map[string]any)
	require.True(f.t, ok)
	includeAcr, ok := acrParameter["value"].(bool)
	if !ok {
		return f.response(request, 400, map[string]any{
			"error": map[string]string{"code": "InvalidTemplate", "message": "offline ARM includeAcr type validation"},
		}), nil
	}
	registryID, registryEndpoint, connectionName := "", "", ""
	if includeAcr {
		registryID, registryEndpoint, connectionName = offlineRegistryID, "crnative.azurecr.io", "crnative-conn"
	}
	if request.Method == http.MethodPost && request.URL.Path == path+"/whatIf" {
		f.previews++
		resourceID := offlineProjectID
		if includeAcr {
			resourceID = registryID
		}
		return f.response(request, 200, map[string]any{
			"status": "Succeeded",
			"properties": map[string]any{"changes": []any{map[string]any{
				"resourceId": resourceID, "changeType": "Create",
			}}},
		}), nil
	}
	require.Equal(f.t, http.MethodPut, request.Method)
	require.Equal(f.t, path, request.URL.Path, "only the subscription deployment may be written")
	f.writes++
	if f.failWrite {
		return f.response(request, 400, map[string]any{
			"error": map[string]string{"code": "DeploymentFailed", "message": "offline deployment failure"},
		}), nil
	}
	output := func(value string) map[string]any { return map[string]any{"type": "String", "value": value} }
	return f.response(request, 200, map[string]any{"id": path, "properties": map[string]any{
		"provisioningState": "Succeeded",
		"outputs": map[string]any{
			"azurE_AI_PROJECT_ID":                  output(offlineProjectID),
			"azurE_AI_PROJECT_NAME":                output("project"),
			"foundrY_PROJECT_ENDPOINT":             output(offlineProjectEndpoint),
			"azurE_CONTAINER_REGISTRY_RESOURCE_ID": output(registryID),
			"azurE_CONTAINER_REGISTRY_ENDPOINT":    output(registryEndpoint),
			"azurE_AI_PROJECT_ACR_CONNECTION_NAME": output(connectionName),
			"azurE_RESOURCE_GROUP":                 output("target-rg"),
			"azurE_FOUNDRY_RESOURCE_GROUP":         output("target-rg"),
		},
	}}), nil
}

func offlineBYOProvider(t *testing.T) (*FoundryProvisioningProvider, *byoARMFake) {
	t.Helper()
	result, err := synthesis.Synthesize(synthesis.Input{ServiceName: "project", RawAzureYAML: []byte(offlineBYOAzureYAML)})
	require.NoError(t, err)
	raw, err := synthesis.ARMTemplate()
	require.NoError(t, err)
	var template map[string]any
	require.NoError(t, json.Unmarshal(raw, &template))
	p := &FoundryProvisioningProvider{
		azdClient:   newResolveEnvTestClient(t, &resolveEnvStubEnvServer{}, &resolveEnvStubPromptServer{}),
		projectPath: t.TempDir(), envName: "test", subID: "target-sub", rgName: "target-rg", location: "westus",
		foundryName: "project", synthResult: result, armTemplate: template, credential: byoTestCredential{},
		tenantID: "user-access-tenant", virtualEnv: map[string]string{},
	}
	fake := &byoARMFake{
		t: t, provider: p,
		clusterProperties: map[string]any{"identityProfile": map[string]any{
			"kubeletidentity": map[string]any{"objectId": offlineKubeletObjectID},
		}},
	}
	p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: fake, Retry: policy.RetryOptions{MaxRetries: -1},
	}}
	return p, fake
}

func TestBYODeployReturnsStandardProjectOutputs(t *testing.T) {
	p, fake := offlineBYOProvider(t)
	server := &resolveEnvStubEnvServer{get: map[string]string{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": offlineRegistryID}}
	p.azdClient = newResolveEnvTestClient(t, server, &resolveEnvStubPromptServer{})
	compiler := &stubCompiler{}
	p.bicepCliInstance = compiler
	result, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	require.NotNil(t, result.Deployment)
	for name, value := range map[string]string{
		"AZURE_AI_PROJECT_ID": offlineProjectID, "FOUNDRY_PROJECT_ENDPOINT": offlineProjectEndpoint,
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": "",
		"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "",
		"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": "",
		"AZURE_TENANT_ID":                      "user-access-tenant",
	} {
		require.Contains(t, result.Deployment.Outputs, name)
		assert.Equal(t, value, result.Deployment.Outputs[name].Value)
	}
	assert.Equal(t, 1, fake.writes)
	assert.Equal(t, []string{
		"GET " + offlineClusterID,
		"GET /subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions",
		"PUT /subscriptions/target-sub/providers/Microsoft.Resources/deployments/" + p.deploymentName(),
	}, fake.requests)
	assert.Empty(t, server.set)
	assert.Empty(t, compiler.buildCalls)
	assert.Empty(t, compiler.buildParamCalls, "embedded provisioning must not invoke a Bicep naming probe")
	assert.NotContains(t, fake.parameters, "kubeletObjectId")
	assert.Contains(t, fake.parameters, "federatedIdentityRoleDefinitionId")
}

func TestBYOPreviewReturnsARMWhatIfChangesWithoutResourceWrites(t *testing.T) {
	p, fake := offlineBYOProvider(t)
	result, err := p.Preview(t.Context(), func(string) {})
	require.NoError(t, err)
	require.NotNil(t, result.Preview)
	require.Len(t, result.Preview.Changes, 1)
	assert.Equal(t, offlineProjectID, result.Preview.Changes[0].ResourceId)
	assert.Equal(t, 1, fake.previews)
	assert.Zero(t, fake.writes)
	assert.Empty(t, p.virtualEnv)
}

func TestBYOFailedDeploymentCanBeRetried(t *testing.T) {
	p, fake := offlineBYOProvider(t)
	fake.failWrite = true
	_, err := p.Deploy(t.Context(), func(string) {})
	require.ErrorContains(t, err, "offline deployment failure")
	fake.failWrite = false
	_, err = p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, 2, fake.writes)
	assert.Len(t, fake.requests, 6)
	assert.Empty(t, p.virtualEnv)
}

func TestBYOAllowsCustomizedOnDiskTemplateAndParameters(t *testing.T) {
	p, fake := offlineBYOProvider(t)
	p.armTemplate["contentVersion"] = "2.0.0.0"
	p.onDiskSource = &templateSource{
		mode: templateModeBicep, armTemplate: p.armTemplate,
		parameters: map[string]any{
			"includeAcr":        map[string]any{"value": true},
			"monitoringMode":    map[string]any{"value": "none"},
			"resourceTokenSalt": map[string]any{"value": "custom"},
			"tags":              map[string]any{"value": map[string]string{"team": "test"}},
		},
	}
	_, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, "2.0.0.0", fake.template["contentVersion"])
	assert.Equal(t, map[string]any{"value": "custom"}, fake.parameters["resourceTokenSalt"])
	assert.Equal(t, map[string]any{"value": true}, fake.parameters["includeAcr"])
	assert.Equal(t, map[string]any{"value": offlineKubeletObjectID}, fake.parameters["kubeletObjectId"])
	assert.Contains(t, fake.requests, "GET "+offlineClusterID)
	p.onDiskSource.parameters["resourceTokenSalt"] = map[string]any{"value": "updated"}
	_, err = p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"value": "updated"}, fake.parameters["resourceTokenSalt"])
	assert.Equal(t, 2, fake.writes)
}

func TestBYONonBooleanIncludeAcrReachesARM(t *testing.T) {
	for _, operation := range []string{"deploy", "preview"} {
		for _, test := range []struct {
			name  string
			value any
		}{
			{"string true", "true"},
			{"string false", "false"},
			{"expression", "[equals(1, 1)]"},
			{"number", 1.0},
			{"null", nil},
		} {
			t.Run(operation+"/"+test.name, func(t *testing.T) {
				p, fake := offlineBYOProvider(t)
				p.onDiskSource = &templateSource{
					mode: templateModeBicep, armTemplate: p.armTemplate,
					parameters: map[string]any{
						"includeAcr":     map[string]any{"value": test.value},
						"monitoringMode": map[string]any{"value": "none"},
					},
				}
				method, suffix := http.MethodPut, ""
				var err error
				if operation == "preview" {
					method, suffix = http.MethodPost, "/whatIf"
					_, err = p.Preview(t.Context(), func(string) {})
				} else {
					_, err = p.Deploy(t.Context(), func(string) {})
				}
				require.ErrorContains(t, err, "InvalidTemplate")
				require.ErrorContains(t, err, "offline ARM includeAcr type validation")
				assert.Equal(t, map[string]any{"value": test.value}, fake.parameters["includeAcr"])
				assert.NotContains(t, fake.parameters, "kubeletObjectId")
				assert.Equal(t, []string{
					"GET " + offlineClusterID,
					"GET /subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions",
					method + " /subscriptions/target-sub/providers/Microsoft.Resources/deployments/" +
						p.deploymentName() + suffix,
				}, fake.requests)
			})
		}
	}
}

func TestNativeFoundryDeployKeepsStandardOutputsWithoutAKSReads(t *testing.T) {
	p, fake := offlineBYOProvider(t)
	result, err := synthesis.Synthesize(synthesis.Input{ServiceName: "project",
		RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n")})
	require.NoError(t, err)
	p.synthResult = result
	deployed, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	require.NotNil(t, deployed.Deployment)
	require.Contains(t, deployed.Deployment.Outputs, "AZURE_AI_PROJECT_ID")
	assert.Equal(t, offlineProjectID, deployed.Deployment.Outputs["AZURE_AI_PROJECT_ID"].Value)
	assert.Len(t, fake.requests, 1, "native Foundry only submits the ARM deployment")
	assert.NotContains(t, fake.parameters, "agentHosting")
	assert.NotContains(t, fake.parameters, "kubeletObjectId")
	assert.Equal(t, 1, fake.writes)
}

func TestBYOPrepareOnlySuppliesDeclaredHostBindings(t *testing.T) {
	p, fake := offlineBYOProvider(t)
	source, err := p.resolveTemplate(t.Context(), func(string) {})
	require.NoError(t, err)
	definitions, ok := source.armTemplate["parameters"].(map[string]any)
	require.True(t, ok)
	delete(definitions, "kubeletObjectId")
	delete(definitions, "federatedIdentityRoleDefinitionId")
	require.NoError(t, p.prepareBYO(t.Context(), source))
	assert.NotContains(t, source.parameters, "kubeletObjectId")
	assert.NotContains(t, source.parameters, "federatedIdentityRoleDefinitionId")
	assert.Len(t, fake.requests, 2, "preparation reads AKS identity and the federation role without a registry")
}

func TestBYOPrepareValidatesKubeletIdentityRegardlessOfACR(t *testing.T) {
	for _, includeAcr := range []bool{false, true} {
		for _, test := range []struct {
			name       string
			properties map[string]any
			expected   string
		}{
			{"missing properties", nil, "requires a kubelet principal object ID"},
			{"missing kubelet identity", map[string]any{}, "requires a kubelet principal object ID"},
			{"missing object ID", map[string]any{"identityProfile": map[string]any{
				"kubeletidentity": map[string]any{},
			}}, "requires a kubelet principal object ID"},
			{"empty object ID", map[string]any{"identityProfile": map[string]any{
				"kubeletidentity": map[string]any{"objectId": ""},
			}}, "must be a GUID"},
			{"invalid object ID", map[string]any{"identityProfile": map[string]any{
				"kubeletidentity": map[string]any{"objectId": "not-a-guid"},
			}}, "must be a GUID"},
		} {
			t.Run(fmt.Sprintf("includeAcr=%t/%s", includeAcr, test.name), func(t *testing.T) {
				p, fake := offlineBYOProvider(t)
				fake.clusterProperties = test.properties
				source, err := p.resolveTemplate(t.Context(), func(string) {})
				require.NoError(t, err)
				source.parameters["includeAcr"] = map[string]any{"value": includeAcr}
				require.ErrorContains(t, p.prepareBYO(t.Context(), source), test.expected)
				assert.Equal(t, []string{"GET " + offlineClusterID}, fake.requests)
			})
		}
	}
}

func TestBYOPrepareValidatesSuppliedKubeletRegardlessOfACR(t *testing.T) {
	for _, includeAcr := range []bool{false, true} {
		for _, test := range []struct {
			name      string
			value     any
			duplicate bool
			expected  string
			requests  int
		}{
			{"empty", "", false, "nonempty string", 0},
			{"whitespace", " ", false, "nonempty string", 0},
			{"non-string", 1, false, "nonempty string", 0},
			{"null", nil, false, "nonempty string", 0},
			{"duplicate", offlineKubeletObjectID, true, "differ only by case", 0},
			{"mismatch", "22222222-2222-4222-8222-222222222222", false, "conflicts", 2},
			{"matching", strings.ToUpper(offlineKubeletObjectID), false, "", 2},
		} {
			t.Run(fmt.Sprintf("includeAcr=%t/%s", includeAcr, test.name), func(t *testing.T) {
				p, fake := offlineBYOProvider(t)
				if test.requests == 0 {
					p.credential = nil
				}
				source, err := p.resolveTemplate(t.Context(), func(string) {})
				require.NoError(t, err)
				source.parameters["includeAcr"] = map[string]any{"value": includeAcr}
				source.parameters["kubeletObjectId"] = map[string]any{"value": test.value}
				if test.duplicate {
					source.parameters["KUBELETOBJECTID"] = map[string]any{"value": test.value}
				}
				err = p.prepareBYO(t.Context(), source)
				assert.Len(t, fake.requests, test.requests)
				if test.expected != "" {
					require.ErrorContains(t, err, test.expected)
					return
				}
				require.NoError(t, err)
				expected := test.value
				if includeAcr {
					expected = offlineKubeletObjectID
				}
				assert.Equal(t, map[string]any{"value": expected}, source.parameters["kubeletObjectId"])
				assert.Equal(t, map[string]any{"value": includeAcr}, source.parameters["includeAcr"])
			})
		}
	}
}

func TestBYORejectsLocalConflictsBeforeAuthentication(t *testing.T) {
	for _, test := range []struct {
		key, expected string
		value         any
	}{
		{"agentHosting", `unknown field "storageAccountResourceId"`,
			map[string]any{"enabled": true, "storageAccountResourceId": "supplied"}},
		{"agentHosting", "conflict with azure.yaml", map[string]any{
			"enabled": true, "hostingType": "ManagedCluster", "name": "default",
			"clusterResourceId": offlineClusterID + "-other",
		}},
	} {
		t.Run(test.key, func(t *testing.T) {
			p, fake := offlineBYOProvider(t)
			p.credential = nil
			source, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			source.parameters[test.key] = map[string]any{"value": test.value}
			require.ErrorContains(t, p.prepareBYO(t.Context(), source), test.expected)
			assert.Empty(t, fake.requests)
		})
	}
}
