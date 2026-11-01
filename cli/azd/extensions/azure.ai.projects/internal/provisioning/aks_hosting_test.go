// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azure.ai.projects/internal/onboarding"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const offlineClusterGroupID = "/subscriptions/cluster-sub/resourceGroups/aks-rg"
const offlineVNetID = "/subscriptions/network-sub/resourceGroups/net-rg/providers/Microsoft.Network/virtualNetworks/net"
const offlineClusterID = offlineClusterGroupID + "/providers/" +
	"Microsoft.ContainerService/managedClusters/aks"
const offlineDeploymentRoleID = "/subscriptions/cluster-sub/providers/Microsoft.Authorization/" +
	"roleDefinitions/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
const offlineProjectID = "/subscriptions/target-sub/resourceGroups/target-rg/providers/" +
	"Microsoft.CognitiveServices/accounts/cog-native/projects/project"
const offlineProjectEndpoint = "https://cog-native.services.ai.azure.com/api/projects/project"
const offlineRegistryID = "/subscriptions/target-sub/resourceGroups/target-rg/providers/" +
	"Microsoft.ContainerRegistry/registries/crnative"
const offlineBYOAzureYAML = `
services:
  project:
    host: azure.ai.project
    agentHosting: {clusterResourceId: ` + offlineClusterID + `}
`

type byoTestCredential struct{}

func (byoTestCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return suppliedCredential{}.GetToken(ctx, options)
}

type byoARMFake struct {
	mu             sync.Mutex
	t              *testing.T
	provider       *FoundryProvisioningProvider
	requests       []string
	parameters     map[string]any
	template       map[string]any
	writes         int
	previews       int
	failWrite      bool
	clusterVersion string
	clusterID      string
	kubeletID      string
	nodeSubnetID   string
	nodePrefix     string
}

func (f *byoARMFake) response(request *http.Request, status int, body any) *http.Response {
	raw, err := json.Marshal(body)
	require.NoError(f.t, err)
	return &http.Response{StatusCode: status, Request: request, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(string(raw)))}
}

func (f *byoARMFake) Do(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request.Method+" "+request.URL.Path)
	if request.Method == http.MethodGet && request.URL.Path == f.clusterID {
		return f.response(request, 200, map[string]any{
			"id": f.clusterID, "location": "westus", "sku": map[string]any{"name": "Base"},
			"identity": map[string]any{"type": "SystemAssigned"},
			"properties": map[string]any{
				"currentKubernetesVersion": f.clusterVersion, "provisioningState": "Succeeded",
				"networkProfile": map[string]any{
					"networkPlugin": "azure", "networkPluginMode": "overlay", "networkDataplane": "cilium",
				},
				"identityProfile": map[string]any{
					"kubeletidentity": map[string]any{"objectId": f.kubeletID},
				},
				"oidcIssuerProfile": map[string]any{"enabled": true, "issuerURL": "https://issuer.example.test/"},
				"securityProfile":   map[string]any{"workloadIdentity": map[string]any{"enabled": true}},
			},
		}), nil
	}
	if request.Method == http.MethodGet && request.URL.Path == f.clusterID+"/agentPools" {
		return f.response(request, 200, map[string]any{"value": []any{
			map[string]any{"id": f.clusterID + "/agentPools/system", "name": "system",
				"properties": map[string]any{
					"mode": "System", "vmSize": "Standard_E4s_v5", "count": 1, "provisioningState": "Succeeded",
				}},
			map[string]any{"id": f.clusterID + "/agentPools/agents", "name": "agents",
				"properties": map[string]any{
					"mode": "User", "osSKU": "AzureLinux", "vmSize": "Standard_D8as_v5", "count": 1,
					"workloadRuntime": "KataVmIsolation", "nodeImageVersion": "AKSAzureLinux-V3katagen2-202608.14.0",
					"nodeLabels":      map[string]any{"agents.ai.azure.com/sandbox-node": "true"},
					"securityProfile": map[string]any{"sshAccess": "Disabled"}, "provisioningState": "Succeeded",
					"vnetSubnetID": f.nodeSubnetID,
				}},
		}}), nil
	}
	if request.Method == http.MethodGet &&
		strings.EqualFold(request.URL.Path, "/subscriptions/cluster-sub/providers/Microsoft.Compute/skus") {
		var values []any
		for _, size := range []struct{ name, cpu string }{{"Standard_E4s_v5", "4"}, {"Standard_D8as_v5", "8"}} {
			values = append(values, map[string]any{
				"name": size.name, "resourceType": "virtualMachines", "locations": []string{"westus"},
				"capabilities": []any{map[string]any{"name": "vCPUs", "value": size.cpu},
					map[string]any{"name": "MemoryGB", "value": "32"}},
			})
		}
		return f.response(request, 200, map[string]any{"value": values}), nil
	}
	if request.Method == http.MethodGet && request.URL.Path == offlineVNetID {
		return f.response(request, 200, map[string]any{
			"id": offlineVNetID, "location": "westus", "properties": map[string]any{
				"provisioningState": "Succeeded", "addressSpace": map[string]any{
					"addressPrefixes": []string{"172.16.0.0/16"},
				},
			},
		}), nil
	}
	if request.Method == http.MethodGet && (request.URL.Path == offlineVNetID+"/subnets/agents" ||
		request.URL.Path == offlineVNetID+"/subnets/nodes" || request.URL.Path == offlineVNetID+"/subnets/pe") {
		prefix := "172.16.0.0/23"
		var delegations []any
		if strings.HasSuffix(request.URL.Path, "/nodes") {
			prefix = f.nodePrefix
		} else if strings.HasSuffix(request.URL.Path, "/pe") {
			prefix = "172.16.3.0/24"
		} else {
			delegations = []any{map[string]any{
				"name": "agents", "properties": map[string]any{"serviceName": "Microsoft.App/environments"},
			}}
		}
		return f.response(request, 200, map[string]any{"id": request.URL.Path, "properties": map[string]any{
			"provisioningState": "Succeeded", "addressPrefix": prefix, "delegations": delegations,
		}}), nil
	}
	if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/subscriptions/") &&
		len(strings.Split(request.URL.Path, "/")) == 3 {
		return f.response(request, 200, map[string]any{"id": request.URL.Path, "tenantId": "tenant"}), nil
	}
	if request.Method == http.MethodGet && strings.Contains(strings.ToLower(request.URL.Path), "/resourcegroups/") &&
		len(strings.Split(request.URL.Path, "/")) == 5 {
		return f.response(request, 200, map[string]any{
			"id": strings.Replace(request.URL.Path, "/resourcegroups/", "/resourceGroups/", 1), "location": "westus",
		}), nil
	}
	if request.Method == http.MethodGet && (strings.Contains(strings.ToLower(request.URL.Path), "/microsoft.storage/") ||
		strings.Contains(strings.ToLower(request.URL.Path), "/microsoft.managedidentity/") ||
		strings.Contains(strings.ToLower(request.URL.Path), "/microsoft.containerregistry/")) {
		return f.response(request, 404, map[string]any{"error": map[string]any{"code": "ResourceNotFound"}}), nil
	}
	if request.Method == http.MethodGet &&
		strings.HasSuffix(strings.ToLower(request.URL.Path), "/providers/microsoft.authorization/roledefinitions") {
		return f.response(request, 200, map[string]any{"value": []any{map[string]any{
			"id": request.URL.Path + "/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			"properties": map[string]any{"roleName": "Managed Identity Federated Identity Credential Contributor",
				"type": "BuiltInRole"},
		}}}), nil
	}
	if request.Method == http.MethodGet &&
		strings.HasSuffix(strings.ToLower(request.URL.Path), "/providers/microsoft.authorization/roleassignments") {
		scope := request.URL.Path[:len(request.URL.Path)-len("/providers/Microsoft.Authorization/roleAssignments")]
		return f.response(request, 200, map[string]any{"value": []any{map[string]any{
			"id": offlineClusterGroupID + "/providers/Microsoft.Authorization/roleAssignments/deployment",
			"properties": map[string]any{
				"scope": scope, "principalId": suppliedOperator,
				"roleDefinitionId": offlineDeploymentRoleID,
			},
		}}}), nil
	}
	if request.Method == http.MethodGet && request.URL.Path == offlineDeploymentRoleID {
		return f.response(request, 200, map[string]any{"id": offlineDeploymentRoleID, "properties": map[string]any{
			"permissions": []any{map[string]any{"actions": []string{"*"}}},
		}}), nil
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
	require.True(f.t, ok)
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
		t: t, provider: p, clusterVersion: "1.31.0", clusterID: offlineClusterID,
		kubeletID: "11111111-1111-4111-8111-111111111111", nodePrefix: "172.16.2.0/24",
	}
	p.bicepCliInstance = &stubCompiler{buildParamResult: bicep.BuildResult{
		Compiled: `{"parametersJson":"{\"parameters\":{\"result\":{\"value\":\"abcdefghijklm\"},` +
			`\"resourceToken\":{\"value\":\"abcdefghijklm\"}}}"}`,
	}}
	p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: fake, Retry: policy.RetryOptions{MaxRetries: -1},
	}}
	return p, fake
}

func TestBYODeployReturnsStandardProjectOutputs(t *testing.T) {
	p, fake := offlineBYOProvider(t)
	server := &resolveEnvStubEnvServer{get: map[string]string{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": offlineRegistryID}}
	p.azdClient = newResolveEnvTestClient(t, server, &resolveEnvStubPromptServer{})
	compiler := p.bicepCliInstance.(*stubCompiler)
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
	assert.Contains(t, fake.requests, "GET "+offlineClusterID)
	assert.Contains(t, fake.requests, "GET "+offlineClusterID+"/agentPools")
	assert.Contains(t, fake.requests, "GET "+offlineClusterGroupID+"/providers/Microsoft.Authorization/roleAssignments")
	assert.Contains(t, fake.requests, "GET "+suppliedGroupID+"/providers/Microsoft.Authorization/roleAssignments")
	for _, request := range fake.requests[:len(fake.requests)-1] {
		assert.True(t, strings.HasPrefix(request, "GET "), request)
	}
	assert.Empty(t, server.set)
	assert.Empty(t, compiler.buildCalls)
	assert.Len(t, compiler.buildParamCalls, 1, "actual template naming is evaluated with the existing Bicep tool")
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
	firstRequests := len(fake.requests)
	fake.failWrite = false
	_, err = p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, 2, fake.writes)
	assert.Len(t, fake.requests, firstRequests*2)
	assert.Empty(t, p.virtualEnv)
}

func TestBYOAllowsCustomizedOnDiskTemplateAndParameters(t *testing.T) {
	p, fake := offlineBYOProvider(t)
	p.armTemplate["contentVersion"] = "2.0.0.0"
	p.onDiskSource = &templateSource{
		mode: templateModeBicep, armTemplate: p.armTemplate,
		parameters: map[string]any{
			"includeAcr":        map[string]any{"value": true},
			"resourceTokenSalt": map[string]any{"value": "custom"},
			"tags":              map[string]any{"value": map[string]string{"team": "test"}},
		},
	}
	_, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, "2.0.0.0", fake.template["contentVersion"])
	assert.Equal(t, map[string]any{"value": "custom"}, fake.parameters["resourceTokenSalt"])
	assert.Equal(t, map[string]any{"value": true}, fake.parameters["includeAcr"])
	assert.Contains(t, fake.parameters, "kubeletObjectId")
	p.onDiskSource.parameters["resourceTokenSalt"] = map[string]any{"value": "updated"}
	_, err = p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"value": "updated"}, fake.parameters["resourceTokenSalt"])
	assert.Equal(t, 2, fake.writes)

	tool := os.Getenv("AZD_BICEP_TOOL_PATH")
	for _, scenario := range []string{
		"renamed resources", "cross subscription", "cross subscription Foundry", "unknown Storage name",
		"unknown Storage and known AKS failure", "known missing Storage reference",
		"unknown workload name", "actual cluster selection", "no native pull assignments",
		"native registry without connection",
		"selected existing network", "new selected subnets", "external DNS", "overlapping selected Node subnet",
		"unknown network injections", "unknown network connections", "unknown network DNS configs",
		"empty network selections", "unrelated network endpoint",
	} {
		t.Run(scenario, func(t *testing.T) {
			if tool == "" {
				t.Skip("set AZD_BICEP_TOOL_PATH to verify real edited Bicep plans offline")
			}
			unknownField := map[string]string{
				"unknown network injections":  "networkInjections",
				"unknown network connections": "privateLinkServiceConnections",
				"unknown network DNS configs": "privateDnsZoneConfigs",
			}[scenario]
			noNetworkSelection := scenario == "empty network selections" || scenario == "unrelated network endpoint"
			provider, requests := offlineBYOProvider(t)
			root := t.TempDir()
			require.NoError(t, fs.WalkDir(synthesis.TemplatesFS(), "templates",
				func(path string, entry fs.DirEntry, err error) error {
					if err != nil || entry.IsDir() {
						return err
					}
					if !strings.HasSuffix(path, ".bicep") && !strings.HasSuffix(path, "abbreviations.json") {
						return nil
					}
					raw, err := synthesis.TemplatesFS().ReadFile(path)
					if err != nil {
						return err
					}
					target := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(path, "templates/")))
					if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
						return err
					}
					return os.WriteFile(target, raw, 0o600)
				}))
			resourcePath := filepath.Join(root, "modules", "resources.bicep")
			//nolint:gosec // Embedded fixture copied to t.TempDir.
			body, err := os.ReadFile(resourcePath)
			require.NoError(t, err)
			edited := string(body)
			for _, replacement := range []struct{ from, to string }{
				{"var foundryAccountName = '${abbrs.cognitiveServicesAccounts}${resourceToken}'",
					"var foundryAccountName = 'edited-account'"},
				{"var storageAccountName = 'st${resourceToken}'", "var storageAccountName = 'editedstore'"},
				{"var hostingIdentityName = '${foundryAccountName}-hosting-mi'",
					"var hostingIdentityName = 'edited-hosting'"},
				{"var workloadIdentityName = '${foundryAccountName}-workload-mi'",
					"var workloadIdentityName = 'edited-workload'"},
				{"name: '${abbrs.containerRegistryRegistries}${resourceToken}'", "name: 'editedregistry'"},
			} {
				require.Contains(t, edited, replacement.from)
				edited = strings.Replace(edited, replacement.from, replacement.to, 1)
			}
			subscription, group := "target-sub", "target-rg"
			if scenario == "cross subscription" {
				subscription, group = "support-sub", "support-rg"
				edited = strings.Replace(edited, "name: 'byo-prerequisites'",
					"name: 'byo-prerequisites'\n  scope: resourceGroup('support-sub', 'support-rg')", 1)
				for _, kind := range []string{"Microsoft.Storage/storageAccounts",
					"Microsoft.ManagedIdentity/userAssignedIdentities"} {
					edited = strings.ReplaceAll(edited, "resourceId('"+kind+"', ",
						"resourceId('support-sub', 'support-rg', '"+kind+"', ")
				}
			}
			if scenario == "cross subscription Foundry" {
				subscription, group = "foundry-sub", "foundry-rg"
				path := filepath.Join(root, "main.bicep")
				//nolint:gosec // Embedded fixture copied to t.TempDir.
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Contains(t, string(raw), "scope: resourceGroup")
				raw = []byte(strings.Replace(string(raw), "scope: resourceGroup",
					"scope: az.resourceGroup('foundry-sub', 'foundry-rg')", 1))
				require.NoError(t, os.WriteFile(path, raw, 0o600))
			}
			if strings.HasPrefix(scenario, "unknown Storage") {
				edited = strings.Replace(edited, "param resourceTokenSalt string = ''",
					"param resourceTokenSalt string = ''\nparam actualStorageName string = 'st${utcNow('MMddHHmmss')}'", 1)
				edited = strings.Replace(edited, "var storageAccountName = 'editedstore'",
					"var storageAccountName = actualStorageName", 1)
			}
			if scenario == "unknown Storage and known AKS failure" {
				requests.clusterVersion = "1.30.9"
			}
			if scenario == "unknown workload name" {
				edited = strings.Replace(edited, "param resourceTokenSalt string = ''",
					"param resourceTokenSalt string = ''\nparam actualWorkloadName string = 'mi${utcNow('MMddHHmmss')}'", 1)
				edited = strings.Replace(edited, "var workloadIdentityName = 'edited-workload'",
					"var workloadIdentityName = actualWorkloadName", 1)
			}
			if scenario == "actual cluster selection" {
				requests.clusterID = offlineClusterID + "-edited"
				requests.kubeletID = "22222222-2222-4222-8222-222222222222"
				edited = strings.ReplaceAll(edited, "clusterResourceId: agentHosting.clusterResourceId",
					"clusterResourceId: '"+requests.clusterID+"'")
			}
			if scenario == "no native pull assignments" {
				path := filepath.Join(root, "modules", "acr.bicep")
				//nolint:gosec // Embedded fixture copied to t.TempDir.
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				lines := strings.Split(string(raw), "\n")
				disabled := 0
				for i, line := range lines {
					if strings.Contains(line, "'Microsoft.Authorization/roleAssignments@") {
						equal := strings.Index(line, " = ")
						require.Greater(t, equal, 0)
						lines[i] = line[:equal] + " = if (false) {"
						disabled++
					}
				}
				require.Equal(t, 2, disabled)
				require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600))
			}
			if scenario == "native registry without connection" {
				path := filepath.Join(root, "modules", "acr.bicep")
				//nolint:gosec // Embedded fixture copied to t.TempDir.
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				text := string(raw)
				start, end := strings.Index(text, "// Existing parent references"), strings.Index(text, "// Outputs")
				require.Greater(t, start, 0)
				require.Greater(t, end, start)
				text = text[:start] + text[end:]
				require.Contains(t, text, "output connectionName string = foundryAccount::project::acrConnection.name")
				text = strings.Replace(text, "output connectionName string = foundryAccount::project::acrConnection.name",
					"output connectionName string = ''", 1)
				require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
			}
			missingStorage := suppliedGroupID + "/providers/Microsoft.Storage/storageAccounts/missingstore"
			if scenario == "known missing Storage reference" {
				start := strings.Index(edited, "var storageAccountResourceId = ")
				end := strings.Index(edited[start:], "\n\n")
				require.Greater(t, start, 0)
				require.Greater(t, end, 0)
				edited = edited[:start] + "var storageAccountResourceId = '" + missingStorage + "'" + edited[start+end:]
			}
			runtimeNetwork := "\nresource runtimeNetworkConfig 'Microsoft.Storage/storageAccounts@2023-05-01' existing = {\n" +
				"  name: 'runtime-network-config'\n}\n"
			if unknownField == "networkInjections" || noNetworkSelection {
				require.Contains(t, edited, "networkInjections: agentNetworkInjections")
				value := "[]"
				if unknownField != "" {
					value = "any(runtimeNetworkConfig.properties)." + unknownField
					edited += runtimeNetwork
				}
				edited = strings.Replace(edited, "networkInjections: agentNetworkInjections",
					"networkInjections: "+value, 1)
			}
			if unknownField == "privateLinkServiceConnections" || unknownField == "privateDnsZoneConfigs" ||
				noNetworkSelection {
				path := filepath.Join(root, "modules", "private-endpoint-dns.bicep")
				//nolint:gosec // Embedded fixture copied to t.TempDir.
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				text := string(raw)
				if scenario == "unrelated network endpoint" {
					require.Contains(t, text, "privateLinkServiceId: aiAccount.id")
					text = strings.Replace(text, "privateLinkServiceId: aiAccount.id",
						"privateLinkServiceId: '${aiAccount.id}-unrelated'", 1)
				} else {
					fields := []string{unknownField}
					if noNetworkSelection {
						fields = []string{"privateLinkServiceConnections", "privateDnsZoneConfigs"}
					}
					for _, field := range fields {
						start := strings.Index(text, "    "+field+": [")
						require.Greater(t, start, 0)
						end := strings.Index(text[start:], "\n    ]")
						require.Greater(t, end, 0)
						end += start + len("\n    ]")
						value := "[]"
						if unknownField != "" {
							value = "any(runtimeNetworkConfig.properties)." + field
						}
						text = text[:start] + "    " + field + ": " + value + text[end:]
					}
					if unknownField != "" {
						text += runtimeNetwork
					}
				}
				require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
			}
			require.NoError(t, os.WriteFile(resourcePath, []byte(edited), 0o600))
			main := filepath.Join(root, "main.bicep")
			//nolint:gosec // Explicit existing offline Bicep tool; fixture paths are under t.TempDir.
			command := exec.CommandContext(t.Context(), tool, "build", main, "--stdout")
			var diagnostics bytes.Buffer
			command.Stderr = &diagnostics
			compiled, err := command.Output()
			require.NoError(t, err, diagnostics.String())
			actual, err := unmarshalARMTemplate(string(compiled), main)
			require.NoError(t, err)
			parameters := map[string]any{
				"includeAcr": map[string]any{"value": true}, "resourceTokenSalt": map[string]any{"value": "edited"},
				"tags": map[string]any{"value": map[string]any{"team": "test"}},
			}
			network := scenario == "selected existing network" || scenario == "new selected subnets" ||
				scenario == "external DNS" || scenario == "overlapping selected Node subnet" ||
				unknownField != "" || noNetworkSelection
			if network {
				requests.nodeSubnetID = offlineVNetID + "/subnets/nodes"
				parameters["includeAcr"] = map[string]any{"value": false}
				for key, value := range map[string]any{
					"enableNetworkIsolation": true, "vnetId": offlineVNetID,
					"agentSubnetName": "agents", "peSubnetName": "pe",
				} {
					parameters[key] = map[string]any{"value": value}
				}
				if scenario == "new selected subnets" {
					parameters["createAgentSubnet"] = map[string]any{"value": true}
					parameters["createPESubnet"] = map[string]any{"value": true}
				} else if scenario == "external DNS" {
					parameters["dnsZonesResourceGroup"] = map[string]any{"value": "dns-rg"}
					parameters["dnsZonesSubscription"] = map[string]any{"value": "dns-sub"}
				} else if scenario == "overlapping selected Node subnet" {
					requests.nodePrefix = "172.16.0.0/24"
				}
			}
			provider.onDiskSource = &templateSource{
				mode: templateModeBicep, sourcePath: main, armTemplate: actual,
				parameters: parameters,
			}
			provider.bicepCliInstance = &stubCompiler{buildParam: func(
				ctx context.Context, path string, env []string,
			) (bicep.BuildResult, error) {
				//nolint:gosec // The provider's local expression probe uses the explicit offline tool.
				command := exec.CommandContext(ctx, tool, "build-params", path, "--stdout")
				command.Env = append(os.Environ(), env...)
				raw, err := command.Output()
				return bicep.BuildResult{Compiled: string(raw)}, err
			}}
			var progress strings.Builder
			if scenario == "cross subscription Foundry" {
				source, err := provider.resolveTemplate(t.Context(), func(string) {})
				require.NoError(t, err)
				identified, err := provider.identifyBYOTemplatePlan(t.Context(), source)
				require.NoError(t, err)
				assert.Equal(t, subscription, identified.SubscriptionID)
				assert.Equal(t, group, identified.ResourceGroup)
				assert.Contains(t, identified.OwningSubscriptions, "target-sub")
				assert.Contains(t, identified.OwningSubscriptions, subscription)
			}
			if network {
				source, err := provider.resolveTemplate(t.Context(), func(string) {})
				require.NoError(t, err)
				identified, err := provider.identifyBYOTemplatePlan(t.Context(), source)
				require.NoError(t, err)
				selection, err := byoDeploymentSelection(source)
				require.NoError(t, err)
				base := identified
				base.Operations = nil
				expected, err := onboarding.WithDeploymentOperations(base, selection)
				require.NoError(t, err)
				if unknownField == "" && !noNetworkSelection || unknownField == "privateLinkServiceConnections" {
					assert.ElementsMatch(t, expected.Operations, identified.Operations,
						"actual standard-network operations must match the reused review planner")
				}
				if unknownField == "networkInjections" || noNetworkSelection {
					assert.Empty(t, identified.AgentSubnet.ID)
				} else {
					assert.Equal(t, expected.AgentSubnet.ID, identified.AgentSubnet.ID)
					assert.Equal(t, expected.AgentSubnet.Supplied, identified.AgentSubnet.Supplied)
				}
				if unknownField != "" {
					var warnings onboarding.Report
					for _, check := range identified.SkippedChecks {
						if check.ID == "P03" && strings.Contains(check.Missing, unknownField) {
							assert.Equal(t, onboarding.Skipped, check.Status)
							assert.False(t, check.Mandatory)
							assert.Contains(t, check.Scope, "/providers/")
							assert.Contains(t, check.Scope, " at ")
							assert.NotEmpty(t, check.Warning)
							warnings = append(warnings, check)
						}
					}
					if unknownField == "privateDnsZoneConfigs" {
						require.Len(t, warnings, 2)
						assert.Equal(t, "Microsoft.Network/privateDnsZones/write", warnings[0].Expected)
						assert.Equal(t, "Microsoft.Network/privateDnsZones/join/action", warnings[1].Expected)
					} else {
						require.Len(t, warnings, 1)
					}
					known := onboarding.Plan{}
					for _, action := range []string{"Microsoft.Network/privateEndpoints/write",
						"Microsoft.Network/privateEndpoints/privateDnsZoneGroups/write"} {
						known.AddOperation("P03", action, suppliedGroupID)
					}
					assert.Subset(t, identified.Operations, known.Operations,
						"unknown array selections must preserve independently known network writes")
				}
			}
			_, err = provider.Deploy(t.Context(), func(message string) { progress.WriteString(message) })
			if scenario == "unknown Storage and known AKS failure" {
				require.ErrorContains(t, err, "A01 FAIL")
				assert.Zero(t, requests.writes)
			} else if scenario == "known missing Storage reference" {
				require.ErrorContains(t, err, "does not exist")
				assert.Contains(t, requests.requests, "GET "+missingStorage)
				assert.Zero(t, requests.writes)
				assert.NotContains(t, progress.String(), "S01 SKIPPED")
			} else if scenario == "overlapping selected Node subnet" {
				require.ErrorContains(t, err, "N02 FAIL")
				assert.Zero(t, requests.writes)
			} else {
				require.NoError(t, err, progress.String())
				assert.Equal(t, 1, requests.writes)
				base := "/subscriptions/" + subscription + "/resourceGroups/" + group + "/providers/"
				assert.Contains(t, requests.requests,
					"GET "+base+"Microsoft.ManagedIdentity/userAssignedIdentities/edited-hosting")
				if scenario != "unknown workload name" {
					assert.Contains(t, requests.requests,
						"GET "+base+"Microsoft.ManagedIdentity/userAssignedIdentities/edited-workload")
				}
				if !network {
					registryGroup := suppliedGroupID
					if scenario == "cross subscription Foundry" {
						registryGroup = "/subscriptions/" + subscription + "/resourceGroups/" + group
					}
					assert.Contains(t, requests.requests,
						"GET "+registryGroup+"/providers/Microsoft.ContainerRegistry/registries/editedregistry")
					assert.Contains(t, progress.String(), "P05 PASS")
					if scenario == "no native pull assignments" {
						assert.Contains(t, progress.String(), "P07 NOT_APPLICABLE")
					} else {
						assert.Contains(t, progress.String(), "P07 PASS")
					}
				}
				assert.Contains(t, progress.String(), "P02 PASS")
			}
			if strings.HasPrefix(scenario, "unknown Storage") {
				assert.Contains(t, progress.String(), "WARNING: P04 [Microsoft.Storage/storageAccounts at ")
				assert.Contains(t, progress.String(), "utcNow")
				assert.Contains(t, progress.String(), "P04 SKIPPED")
				assert.NotContains(t, progress.String(), "P04 PASS")
				assert.Contains(t, progress.String(), "P02 PASS")
			}
			if scenario == "unknown workload name" {
				assert.Contains(t, progress.String(), "P02 SKIPPED")
				assert.Contains(t, progress.String(), "utcNow")
				assert.Equal(t, map[string]any{"value": "/subscriptions/target-sub/providers/" +
					"Microsoft.Authorization/roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
					requests.parameters["federatedIdentityRoleDefinitionId"])
				assert.Contains(t, progress.String(), "I02 PASS [/subscriptions/target-sub]")
				assert.Contains(t, progress.String(), "P04 PASS")
			}
			if scenario == "actual cluster selection" {
				assert.Contains(t, requests.requests, "GET "+requests.clusterID)
				assert.NotContains(t, requests.requests, "GET "+offlineClusterID,
					"the account's actual cluster controls AKS evidence, not the YAML selector")
				assert.Equal(t, map[string]any{"value": requests.kubeletID}, requests.parameters["kubeletObjectId"])
			}
			if network {
				assert.NotContains(t, progress.String(), "P05 ")
				assert.NotContains(t, progress.String(), "P07 ")
				if noNetworkSelection {
					assert.NotContains(t, progress.String(), "P03 ")
					assert.Contains(t, progress.String(), "N02 NOT_APPLICABLE")
				} else {
					assert.Contains(t, progress.String(), "P03 PASS")
					if unknownField != "" {
						assert.Contains(t, progress.String(), "P03 SKIPPED")
						assert.Contains(t, progress.String(), "WARNING: P03 [")
						assert.Contains(t, progress.String(), unknownField)
						for _, action := range []string{"Microsoft.Network/privateEndpoints/write",
							"Microsoft.Network/privateEndpoints/privateDnsZoneGroups/write"} {
							assert.Contains(t, progress.String(), "P03 PASS ["+suppliedGroupID+"]\n  Expected: "+action)
						}
					} else {
						assert.NotContains(t, progress.String(), "P03 SKIPPED")
					}
				}
				if scenario == "new selected subnets" {
					assert.Contains(t, progress.String(), "N02 NOT_APPLICABLE")
					assert.NotContains(t, requests.requests, "GET "+offlineVNetID+"/subnets/agents")
				} else if unknownField == "networkInjections" {
					assert.Contains(t, progress.String(), "N02 SKIPPED")
				} else if scenario != "overlapping selected Node subnet" && !noNetworkSelection {
					assert.Contains(t, progress.String(), "N02 PASS")
				}
			}
			assert.NotContains(t, requests.requests, "GET "+defaultStorageID,
				"an edited or unknown name must not fall back to the standard naming formula")
			assert.NotContains(t, requests.requests, "GET "+defaultWorkloadID)
		})
	}
}

func TestNativeFoundryDeployKeepsStandardOutputsWithoutAKSReads(t *testing.T) {
	p, fake := offlineBYOProvider(t)
	result, err := synthesis.Synthesize(synthesis.Input{ServiceName: "project",
		RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n")})
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
	require.NoError(t, p.prepareBYO(t.Context(), source, func(string) {}))
	assert.NotContains(t, source.parameters, "kubeletObjectId")
	assert.NotContains(t, source.parameters, "federatedIdentityRoleDefinitionId")
	for _, request := range fake.requests {
		assert.True(t, strings.HasPrefix(request, "GET "), request)
	}
	assert.Zero(t, fake.writes)
	assert.Zero(t, fake.previews)
}

func TestBYORejectsLocalConflictsBeforeAuthentication(t *testing.T) {
	for _, test := range []struct {
		key, expected string
		value         any
	}{
		{"includeAcr", "includeAcr must be a boolean", "false"},
		{"agentHosting", "conflict with azure.yaml",
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
			require.ErrorContains(t, p.prepareBYO(t.Context(), source, func(string) {}), test.expected)
			assert.Empty(t, fake.requests)
		})
	}
}
func TestBYOInitializeRejectsInvalidInputsBeforePromptsOrPersistence(t *testing.T) {
	for _, onDisk := range []bool{false, true} {
		root := t.TempDir()
		config := "services:\n  project:\n    host: azure.ai.project\n" +
			"    agentHosting: {clusterResourceId: " + offlineClusterID + ", storageAccountResourceId: []}\n"
		path := filepath.Join(root, "azure.yaml")
		require.NoError(t, os.WriteFile(path, []byte(config), 0o600))
		if onDisk {
			require.NoError(t, os.Mkdir(filepath.Join(root, "infra"), 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(root, "infra", "main.bicep"), []byte("// generated"), 0o600))
		}
		env := &resolveEnvStubEnvServer{envName: "test"}
		prompt := &resolveEnvStubPromptServer{}
		p := &FoundryProvisioningProvider{azdClient: newResolveEnvTestClient(t, env, prompt)}
		err := p.Initialize(t.Context(), root, &azdext.ProvisioningOptions{Provider: FoundryProviderName})
		require.ErrorContains(t, err, "storageAccountResourceId: expected a nonempty resource ID")
		assert.Empty(t, env.set)
		assert.Zero(t, prompt.subscriptionN)
		assert.Zero(t, prompt.locationN)
		assert.Nil(t, p.credential)
		//nolint:gosec // Test-owned azure.yaml under t.TempDir.
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, config, string(raw))
	}
}
