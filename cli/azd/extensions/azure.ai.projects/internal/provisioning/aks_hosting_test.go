// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS kubelet kubeletidentity crnative AGENTHOSTING
// cspell:ignore INCLUDEACR KUBELETOBJECTID

package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/synthesis"
	"azure.ai.projects/synthesis/testutil"

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
	clusterSKUName string
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
			"id": f.clusterID, "location": "westus",
			"sku":      map[string]any{"name": f.clusterSKUName},
			"identity": map[string]any{"type": "SystemAssigned"},
			"properties": map[string]any{
				"currentKubernetesVersion": f.clusterVersion,
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
					"mode": "System", "vmSize": "Standard_E4s_v5", "count": 1,
				}},
			map[string]any{"id": f.clusterID + "/agentPools/agents", "name": "agents",
				"properties": map[string]any{
					"mode": "User", "osSKU": "AzureLinux", "vmSize": "Standard_D8as_v5", "count": 1,
					"workloadRuntime": "KataVmIsolation", "nodeImageVersion": "AKSAzureLinux-V3katagen2-202608.14.0",
					"nodeLabels":   map[string]any{"agents.ai.azure.com/sandbox-node": "true"},
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
	if request.Method == http.MethodGet &&
		strings.HasSuffix(strings.ToLower(request.URL.Path),
			strings.ToLower("/providers/Microsoft.Authorization/roleAssignments")) {
		return f.response(request, 200, map[string]any{"value": []any{}}), nil
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
	path := "/subscriptions/target-sub/providers/Microsoft.Resources/deployments/" + f.provider.deploymentName()
	if request.Method == http.MethodGet && request.URL.Path == path {
		return f.response(request, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "DeploymentNotFound", "message": "no previous deployment"},
		}), nil
	}
	if request.Method == http.MethodGet && request.URL.Path == offlineProjectID {
		return f.response(request, http.StatusOK, map[string]any{
			"identity": map[string]string{"principalId": "22222222-2222-4222-8222-222222222222"},
		}), nil
	}
	require.NotNil(f.t, request.Body, "unexpected ARM request: %s %s", request.Method, request.URL.Path)
	var submitted struct {
		Properties struct {
			Template   map[string]any `json:"template"`
			Parameters map[string]any `json:"parameters"`
		} `json:"properties"`
	}
	require.NoError(f.t, json.NewDecoder(request.Body).Decode(&submitted))
	f.template, f.parameters = submitted.Properties.Template, submitted.Properties.Parameters
	definitions, ok := f.template["parameters"].(map[string]any)
	require.True(f.t, ok)
	for name := range f.parameters {
		declared := false
		for definition := range definitions {
			if strings.EqualFold(name, definition) {
				declared = true
				break
			}
		}
		if !declared {
			return f.response(request, 400, map[string]any{
				"error": map[string]string{
					"code": "InvalidTemplate", "message": "offline ARM undeclared parameter: " + name,
				},
			}), nil
		}
	}
	acrParameter, ok := f.parameters["includeAcr"].(map[string]any)
	if !ok {
		return f.response(request, 400, map[string]any{
			"error": map[string]string{"code": "InvalidTemplate", "message": "offline ARM includeAcr type validation"},
		}), nil
	}
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
	t.Setenv(synthesis.BYOAKSEnvVar, "true")
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
		t: t, provider: p, clusterVersion: "1.31.0", clusterSKUName: "Base", clusterID: offlineClusterID,
		kubeletID: offlineKubeletObjectID, nodePrefix: "172.16.2.0/24",
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

func requireBYOFeatureNotEnabled(t *testing.T, err error) {
	t.Helper()
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected a BYO AKS feature error, got %v", err)
	assert.Same(t, local, err, "the structured error must be returned unchanged")
	assert.Equal(t, exterrors.CodeFeatureNotEnabled, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryCompatibility, local.Category)
	assert.Equal(t, fmt.Sprintf("preview feature %q is disabled", synthesis.BYOAKSFeatureID), local.Message)
	assert.Contains(t, local.Suggestion, synthesis.BYOAKSEnvVar+"=true")
}

func TestBYOPrepareFeatureGateUsesProcessEnvironment(t *testing.T) {
	for _, test := range []struct {
		name, value    string
		unset, enabled bool
	}{
		{"unset", "", true, false},
		{"empty", "", false, false},
		{"false", "false", false, false},
		{"zero", "0", false, false},
		{"f", "f", false, false},
		{"F", "F", false, false},
		{"FALSE", "FALSE", false, false},
		{"False", "False", false, false},
		{"invalid", "invalid", false, false},
		{"on", "on", false, false},
		{"yes", "yes", false, false},
		{"mixed case", "TrUe", false, false},
		{"whitespace", " true ", false, false},
		{"true", "true", false, true},
		{"one", "1", false, true},
		{"t", "t", false, true},
		{"T", "T", false, true},
		{"TRUE", "TRUE", false, true},
		{"True", "True", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, fake, _ := suppliedProvider(t, 7)
			t.Setenv(synthesis.BYOAKSEnvVar, test.value)
			if test.unset {
				require.NoError(t, os.Unsetenv(synthesis.BYOAKSEnvVar))
			}
			p.virtualEnv[synthesis.BYOAKSEnvVar] = "true"
			source, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			original, err := json.Marshal(source.parameters)
			require.NoError(t, err)
			if !test.enabled {
				p.credential = nil
			}

			err = p.prepareBYO(t.Context(), source, func(string) {})
			if test.enabled {
				require.NoError(t, err)
				assert.Subset(t, fake.reads, []string{
					offlineClusterID,
					"/subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions",
					suppliedHostingID,
					suppliedWorkloadID,
				}, fake.reads)
				assert.Equal(t, map[string]any{"value": suppliedHostingPrincipal}, source.parameters["hostingPrincipalId"])
				assert.Equal(t, map[string]any{"value": suppliedWorkloadPrincipal}, source.parameters["workloadPrincipalId"])
				assert.Contains(t, source.parameters, "federatedIdentityRoleDefinitionId")
			} else {
				requireBYOFeatureNotEnabled(t, err)
				assert.Nil(t, p.credential)
				assert.Empty(t, fake.reads)
				assert.Empty(t, fake.base.requests)
				current, err := json.Marshal(source.parameters)
				require.NoError(t, err)
				assert.JSONEq(t, string(original), string(current))
			}
			assert.Equal(t, map[string]string{synthesis.BYOAKSEnvVar: "true"}, p.virtualEnv)
			assert.Zero(t, fake.base.writes)
			assert.Zero(t, fake.base.previews)
		})
	}
}

func TestBYOProvisioningFeatureGateUsesEffectiveParameters(t *testing.T) {
	hosting := map[string]any{"enabled": true, "clusterResourceId": offlineClusterID}
	disabledHosting := map[string]any{"enabled": false, "clusterResourceId": offlineClusterID}
	for _, preview := range []bool{false, true} {
		for _, test := range []struct {
			name                    string
			mode                    templateMode
			yamlHosting             bool
			parameter, defaultValue any
		}{
			{name: "embedded", yamlHosting: true},
			{name: "JSON parameters", mode: templateModeBicep, parameter: hosting},
			{name: "Bicep parameters", mode: templateModeBicepParam, parameter: hosting},
			{name: "template default", mode: templateModeBicep, defaultValue: hosting},
			{name: "YAML overrides disabled default", mode: templateModeBicep, yamlHosting: true,
				defaultValue: disabledHosting},
		} {
			t.Run(fmt.Sprintf("preview=%t/%s", preview, test.name), func(t *testing.T) {
				p, fake := offlineBYOProvider(t)
				t.Setenv(synthesis.BYOAKSEnvVar, "false")
				p.virtualEnv[synthesis.BYOAKSEnvVar] = "true"
				server := &resolveEnvStubEnvServer{get: map[string]string{synthesis.BYOAKSEnvVar: "true"}}
				p.azdClient = newResolveEnvTestClient(t, server, &resolveEnvStubPromptServer{})
				rawYAML := "services:\n  project:\n    host: azure.ai.project\n"
				if test.yamlHosting {
					rawYAML = offlineBYOAzureYAML
				}
				rawYAML += "    env:\n      " + synthesis.BYOAKSEnvVar + ": 'true'\n"
				result, err := synthesis.Synthesize(synthesis.Input{ServiceName: "project", RawAzureYAML: []byte(rawYAML)})
				require.NoError(t, err)
				p.synthResult = result
				original, err := json.Marshal(p.synthResult)
				require.NoError(t, err)

				if test.mode != templateModeEmbedded {
					if test.defaultValue != nil {
						definitions, ok := p.armTemplate["parameters"].(map[string]any)
						require.True(t, ok)
						definition, ok := definitions["agentHosting"].(map[string]any)
						require.True(t, ok)
						definition["defaultValue"] = test.defaultValue
					}
					template, err := json.Marshal(p.armTemplate)
					require.NoError(t, err)
					parameters := map[string]any{}
					if test.parameter != nil {
						parameters["AGENTHOSTING"] = test.parameter
					}
					params := minimalARMParametersFile(t, parameters)
					infraDir := filepath.Join(p.projectPath, onDiskInfraDir)
					require.NoError(t, os.MkdirAll(infraDir, 0o750))
					compiler := &stubCompiler{}
					p.bicepCliInstance = compiler
					if test.mode == templateModeBicep {
						require.NoError(t, os.WriteFile(
							filepath.Join(infraDir, onDiskBicepFile), []byte("// fixture\n"), 0o600,
						))
						require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskParamsFile), []byte(params), 0o600))
						compiler.buildResult = bicep.BuildResult{Compiled: string(template)}
					} else {
						require.NoError(t, os.WriteFile(
							filepath.Join(infraDir, onDiskBicepParamFile), []byte("// fixture\n"), 0o600,
						))
						envelope, err := json.Marshal(map[string]string{
							"templateJson": string(template), "parametersJson": params,
						})
						require.NoError(t, err)
						compiler.buildParamResult = bicep.BuildResult{Compiled: string(envelope)}
					}
				}

				if preview {
					_, previewErr := p.Preview(t.Context(), func(string) {})
					err = previewErr
				} else {
					_, deployErr := p.Deploy(t.Context(), func(string) {})
					err = deployErr
				}
				assert.Empty(t, fake.requests, "a disabled BYO AKS feature must not read or deploy")
				assert.Zero(t, fake.writes)
				assert.Zero(t, fake.previews)
				requireBYOFeatureNotEnabled(t, err)
				current, err := json.Marshal(p.synthResult)
				require.NoError(t, err)
				assert.JSONEq(t, string(original), string(current), "the gate must not rewrite hosting configuration")
				assert.Equal(t, map[string]string{synthesis.BYOAKSEnvVar: "true"}, p.virtualEnv)
				assert.Empty(t, server.set)
			})
		}
	}
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
	for _, request := range fake.requests[:len(fake.requests)-1] {
		assert.True(t, strings.HasPrefix(request, "GET "), request)
	}
	assert.Empty(t, server.set)
	assert.Empty(t, compiler.buildCalls)
	assert.NotEmpty(t, compiler.buildParamCalls, "actual template naming is evaluated with the existing Bicep tool")
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

func TestBYORegistryValidationBeforeLifecycleWrites(t *testing.T) {
	const registryID = "/subscriptions/target-sub/resourceGroups/shared/providers/" +
		"Microsoft.ContainerRegistry/registries/registry"
	for _, test := range []struct {
		name, expectedError   string
		abac, remote, invalid bool
	}{
		{name: "ABAC remote", abac: true, remote: true, expectedError: "does not authenticate"},
		{name: "ABAC local", abac: true, expectedError: "check existing registry pull access"},
		{name: "classic remote", remote: true, expectedError: "check existing registry pull access"},
		{name: "invalid hosting", invalid: true, expectedError: "aks-version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(synthesis.ACRReuseEnvVar, "true")
			p, fake := offlineBYOProvider(t)
			env := &resolveEnvStubEnvServer{envName: "test"}
			p.azdClient = newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
			p.acrRemoteBuild = test.remote
			p.acrPromptedEnv = map[string]string{envKeySubscriptionID: p.subID, envKeyLocation: p.location}
			p.synthResult.Parameters["includeAcr"] = true
			p.synthResult.Parameters["existingAcrResourceId"] = registryID
			if test.invalid {
				fake.clusterVersion = "1.30.0"
			}
			p.resourceGroupState = func(context.Context) (map[string]*string, bool, error) {
				return nil, true, nil
			}
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.Equal(t, http.MethodGet, method)
				if path != registryID {
					return &azcore.ResponseError{StatusCode: http.StatusNotFound}
				}
				mode := "LegacyRegistryPermissions"
				if test.abac {
					mode = "AbacRepositoryPermissions"
				}
				return acrTestReply(out, map[string]any{"properties": map[string]any{"roleAssignmentMode": mode}})
			}
			fake.failWrite = true
			_, err := p.Deploy(t.Context(), func(string) {})
			require.ErrorContains(t, err, test.expectedError)
			if test.abac && test.remote {
				assert.Empty(t, fake.requests)
			} else {
				assert.NotEmpty(t, fake.requests)
			}
			assert.Zero(t, fake.writes)
			assert.Empty(t, env.set)
			assert.Empty(t, env.config)
			assert.Nil(t, p.acrPending)
			assert.Len(t, p.acrPromptedEnv, 2)
		})
	}
}

func TestBYORegistryPreviewPlansKubeletAccessWithoutWrites(t *testing.T) {
	const registryID = "/subscriptions/target-sub/resourceGroups/shared/providers/" +
		"Microsoft.ContainerRegistry/registries/registry"
	for _, abac := range []bool{false, true} {
		t.Run(fmt.Sprintf("ABAC=%t", abac), func(t *testing.T) {
			t.Setenv(synthesis.ACRReuseEnvVar, "true")
			p, fake := offlineBYOProvider(t)
			env := &resolveEnvStubEnvServer{envName: "test"}
			p.azdClient = newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
			p.acrRemoteBuild = !abac
			p.synthResult.Parameters["includeAcr"] = true
			p.synthResult.Parameters["existingAcrResourceId"] = registryID
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.Equal(t, http.MethodGet, method)
				if strings.HasPrefix(path, registryID+"/providers/Microsoft.Authorization/roleAssignments?") {
					assignment := acrTestAssignment()
					assignment.Properties.Scope = registryID
					assignment.Properties.PrincipalID = "22222222-2222-4222-8222-222222222222"
					values := []acrRoleAssignment{assignment}
					if abac {
						assignment.Properties.PrincipalID = offlineKubeletObjectID
						assignment.Properties.RoleDefinitionID = "/subscriptions/target-sub/providers/" +
							"Microsoft.Authorization/roleDefinitions/" + acrRepositoryReader
						values = append(values, assignment)
					}
					return acrTestReply(out, map[string]any{"value": values})
				}
				if path != registryID {
					return &azcore.ResponseError{StatusCode: http.StatusNotFound}
				}
				mode := "LegacyRegistryPermissions"
				if abac {
					mode = "AbacRepositoryPermissions"
				}
				return acrTestReply(out, map[string]any{"properties": map[string]any{"roleAssignmentMode": mode}})
			}
			_, err := p.Preview(t.Context(), func(string) {})
			require.NoError(t, err)
			assert.Equal(t, 1, fake.previews)
			assert.Zero(t, fake.writes)
			assert.NotEmpty(t, fake.requests)
			assert.Empty(t, env.set)
			assert.Empty(t, env.config)
			assert.Nil(t, p.acrPending)
			assert.Equal(t, map[string]any{"value": offlineKubeletObjectID}, fake.parameters["acrPullPrincipalId"])
			assert.Equal(t, map[string]any{"value": abac}, fake.parameters["acrPullAssigned"])
			role := acrPullRole
			if abac {
				role = acrRepositoryReader
			}
			assert.Equal(t, map[string]any{"value": "/subscriptions/target-sub/providers/" +
				"Microsoft.Authorization/roleDefinitions/" + role}, fake.parameters["acrPullRoleDefinitionId"])
		})
	}
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
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
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
	assert.Contains(t, fake.parameters, "kubeletObjectId")
	p.onDiskSource.parameters["resourceTokenSalt"] = map[string]any{"value": "updated"}
	_, err = p.Deploy(t.Context(), func(string) {})
	require.ErrorContains(t, err, "retain the deployed resourceTokenSalt")
	assert.Equal(t, 1, fake.writes)
	p.onDiskSource.parameters["resourceTokenSalt"] = map[string]any{"value": "custom"}
	p.onDiskSource.parameters["tags"] = map[string]any{"value": map[string]string{"team": "updated"}}
	_, err = p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"value": "custom"}, fake.parameters["resourceTokenSalt"])
	assert.Equal(t, map[string]any{"value": map[string]any{"team": "updated"}}, fake.parameters["tags"])
	assert.Equal(t, 2, fake.writes)

	for _, scenario := range []string{
		"renamed resources", "cross subscription", "cross subscription Foundry", "unknown Storage name",
		"unknown Storage and known AKS failure", "known missing Storage reference",
		"unknown workload name", "actual cluster selection",
		"cross subscription unknown workload",
		"selected existing network", "new selected subnets", "overlapping selected Node subnet",
		"unknown network injections", "empty network selections",
	} {
		t.Run(scenario, func(t *testing.T) {
			tool := testutil.LookupBicep(t)
			unknownField := map[string]string{
				"unknown network injections": "networkInjections",
			}[scenario]
			noNetworkSelection := scenario == "empty network selections"
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
					//nolint:gosec // G703: embedded fixture path under t.TempDir.
					return os.WriteFile(target, raw, 0o600)
				}))
			resourcePath := filepath.Join(root, "modules", "resources.bicep")
			//nolint:gosec // Embedded fixture copied to t.TempDir.
			body, err := os.ReadFile(resourcePath)
			require.NoError(t, err)
			edited := strings.ReplaceAll(string(body), "\r\n", "\n")
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
			subscription := "target-sub"
			if scenario == "cross subscription" || scenario == "cross subscription unknown workload" {
				subscription = "support-sub"
				edited = strings.Replace(edited, "name: 'byo-prerequisites'",
					"name: 'byo-prerequisites'\n  scope: resourceGroup('support-sub', 'support-rg')", 1)
				for _, kind := range []string{"Microsoft.Storage/storageAccounts",
					"Microsoft.ManagedIdentity/userAssignedIdentities"} {
					edited = strings.ReplaceAll(edited, "resourceId('"+kind+"', ",
						"resourceId('support-sub', 'support-rg', '"+kind+"', ")
				}
			}
			if scenario == "cross subscription Foundry" {
				subscription = "foundry-sub"
				path := filepath.Join(root, "main.bicep")
				//nolint:gosec // Embedded fixture copied to t.TempDir.
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Contains(t, string(raw), "scope: resourceGroup")
				raw = []byte(strings.Replace(string(raw), "scope: resourceGroup",
					"scope: az.resourceGroup('foundry-sub', 'foundry-rg')", 1))
				//nolint:gosec // G703: test-owned main.bicep under t.TempDir.
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
			if scenario == "unknown workload name" || scenario == "cross subscription unknown workload" {
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
			missingStorage := suppliedGroupID + "/providers/Microsoft.Storage/storageAccounts/missingstore"
			if scenario == "known missing Storage reference" {
				start := strings.Index(edited, "var storageAccountResourceId = ")
				end := strings.Index(edited[start:], "\n\n")
				require.Greater(t, start, 0)
				require.Greater(t, end, 0)
				edited = edited[:start] + "var storageAccountResourceId = '" + missingStorage + "'" + edited[start+end:]
			}
			runtimeNetwork := "\nresource runtimeNetworkConfig " +
				"'Microsoft.Storage/storageAccounts@2023-05-01' existing = {\n" +
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
			//nolint:gosec // G703: test-owned resources.bicep under t.TempDir.
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
				"tags":           map[string]any{"value": map[string]any{"team": "test"}},
				"monitoringMode": map[string]any{"value": "none"},
			}
			network := scenario == "selected existing network" || scenario == "new selected subnets" ||
				scenario == "overlapping selected Node subnet" ||
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
				identified, err := provider.identifyBYOTemplatePlan(t.Context(), source, nil)
				require.NoError(t, err)
				assert.Equal(t, subscription, identified.SubscriptionID)
				assert.Contains(t, identified.OwningSubscriptions, "target-sub")
				assert.Contains(t, identified.OwningSubscriptions, subscription)
			}
			if network {
				source, err := provider.resolveTemplate(t.Context(), func(string) {})
				require.NoError(t, err)
				identified, err := provider.identifyBYOTemplatePlan(t.Context(), source, nil)
				require.NoError(t, err)
				if unknownField == "networkInjections" || noNetworkSelection {
					assert.Empty(t, identified.AgentSubnet.ID)
				} else {
					assert.Equal(t, offlineVNetID+"/subnets/agents", identified.AgentSubnet.ID)
					assert.Equal(t, scenario != "new selected subnets", identified.AgentSubnet.Supplied)
				}
			}
			_, err = provider.Deploy(t.Context(), func(message string) { progress.WriteString(message) })
			if scenario == "unknown Storage and known AKS failure" {
				require.ErrorContains(t, err, "aks-version FAIL")
				assert.Zero(t, requests.writes)
			} else if scenario == "known missing Storage reference" {
				require.ErrorContains(t, err, "does not exist")
				assert.Contains(t, requests.requests, "GET "+missingStorage)
				assert.Zero(t, requests.writes)
				assert.NotContains(t, provider.preflightReport.String(), "storage-type SKIPPED")
			} else if scenario == "overlapping selected Node subnet" {
				require.ErrorContains(t, err, "agent-subnet FAIL")
				assert.Zero(t, requests.writes)
			} else {
				require.NoError(t, err, progress.String())
				assert.Equal(t, 1, requests.writes)
			}
			if strings.HasPrefix(scenario, "unknown Storage") {
				assert.Contains(t, provider.preflightReport.String(), "utcNow")
			}
			if scenario == "unknown workload name" || scenario == "cross subscription unknown workload" {
				assert.Contains(t, provider.preflightReport.String(), "utcNow")
				assert.Equal(t, map[string]any{"value": "/subscriptions/" + subscription + "/providers/" +
					"Microsoft.Authorization/roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
					requests.parameters["federatedIdentityRoleDefinitionId"])
				assert.Contains(t, provider.preflightReport.String(), "resource-refs PASS [/subscriptions/target-sub]")
			}
			if scenario == "actual cluster selection" {
				assert.Contains(t, requests.requests, "GET "+requests.clusterID)
				assert.NotContains(t, requests.requests, "GET "+offlineClusterID,
					"the account's actual cluster controls AKS evidence, not the YAML selector")
				assert.Equal(t, map[string]any{"value": requests.kubeletID}, requests.parameters["kubeletObjectId"])
			}
			if network {
				if noNetworkSelection {
					assert.Contains(t, provider.preflightReport.String(), "agent-subnet NOT_APPLICABLE")
				}
				if scenario == "new selected subnets" {
					assert.Contains(t, provider.preflightReport.String(), "agent-subnet NOT_APPLICABLE")
					assert.NotContains(t, requests.requests, "GET "+offlineVNetID+"/subnets/agents")
				} else if unknownField == "networkInjections" {
					assert.Contains(t, provider.preflightReport.String(), "agent-subnet SKIPPED")
				} else if scenario != "overlapping selected Node subnet" && !noNetworkSelection {
					assert.Contains(t, provider.preflightReport.String(), "agent-subnet PASS")
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
	t.Setenv(synthesis.BYOAKSEnvVar, "false")
	result, err := synthesis.Synthesize(synthesis.Input{ServiceName: "project",
		RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n")})
	require.NoError(t, err)
	p.synthResult = result
	deployed, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	require.NotNil(t, deployed.Deployment)
	require.Contains(t, deployed.Deployment.Outputs, "AZURE_AI_PROJECT_ID")
	assert.Equal(t, offlineProjectID, deployed.Deployment.Outputs["AZURE_AI_PROJECT_ID"].Value)
	assert.Len(t, fake.requests, 2, "native Foundry reads deployment recovery state before submitting")
	assert.NotContains(t, fake.parameters, "agentHosting")
	assert.NotContains(t, fake.parameters, "kubeletObjectId")
	assert.Equal(t, 1, fake.writes)
}

func TestBYOPrepareOnlySuppliesDeclaredHostBindings(t *testing.T) {
	p, supplied, _ := suppliedProvider(t, 7)
	fake := supplied.base
	source, err := p.resolveTemplate(t.Context(), func(string) {})
	require.NoError(t, err)
	definitions, ok := source.armTemplate["parameters"].(map[string]any)
	require.True(t, ok)
	for _, name := range []string{
		"kubeletObjectId", "federatedIdentityRoleDefinitionId", "hostingPrincipalId", "workloadPrincipalId",
	} {
		delete(definitions, name)
	}
	require.NoError(t, p.prepareBYO(t.Context(), source, func(string) {}))
	assert.NotContains(t, source.parameters, "kubeletObjectId")
	assert.NotContains(t, source.parameters, "federatedIdentityRoleDefinitionId")
	assert.NotContains(t, source.parameters, "hostingPrincipalId")
	assert.NotContains(t, source.parameters, "workloadPrincipalId")
	for _, request := range fake.requests {
		assert.True(t, strings.HasPrefix(request, "GET "), request)
	}
	assert.Zero(t, fake.writes)
	assert.Zero(t, fake.previews)
}

func TestBYOPreflightAcceptsExternalRegistry(t *testing.T) {
	for _, selectedBy := range []string{"parameter", "template default", "binding"} {
		t.Run(selectedBy, func(t *testing.T) {
			p, fake := offlineBYOProvider(t)
			source, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			switch selectedBy {
			case "parameter":
				source.parameters["existingAcrResourceId"] = map[string]any{"value": testACRID}
			case "template default":
				definitions, ok := source.armTemplate["parameters"].(map[string]any)
				require.True(t, ok)
				definitions["existingAcrResourceId"] = map[string]any{"defaultValue": testACRID}
			case "binding":
				p.acrBinding = acrTestBinding()
			}
			require.NoError(t, p.prepareBYO(t.Context(), source, func(string) {}))
			assert.NotEmpty(t, fake.requests)
		})
	}
}

func TestBYORejectsLocalConflictsBeforeAuthentication(t *testing.T) {
	for _, test := range []struct {
		key, expected string
		value         any
	}{
		{"agentHosting", "invalid agentHosting deployment parameters", "not-an-object"},
		{"agentHosting", "invalid agentHosting deployment parameters", map[string]any{"enabled": "true"}},
		{"agentHosting", "clusterResourceId must be a resource ID string", map[string]any{
			"enabled": true, "clusterResourceId": 42,
		}},
		{"agentHosting", "expected a complete Microsoft.ContainerService/managedClusters resource ID", map[string]any{
			"enabled": true, "clusterResourceId": "/not-a-cluster",
		}},
		{"agentHosting", "expected a complete Microsoft.ManagedIdentity/userAssignedIdentities resource ID", map[string]any{
			"enabled": true, "clusterResourceId": offlineClusterID,
			"hostingManagementIdentityResourceId": "/not-an-identity",
		}},
		{"agentHosting", "hostingManagementIdentityResourceId must be a resource ID string", map[string]any{
			"enabled": true, "clusterResourceId": offlineClusterID, "hostingManagementIdentityResourceId": 42,
		}},
	} {
		t.Run(test.key, func(t *testing.T) {
			p, fake := offlineBYOProvider(t)
			t.Setenv(synthesis.BYOAKSEnvVar, "false")
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
