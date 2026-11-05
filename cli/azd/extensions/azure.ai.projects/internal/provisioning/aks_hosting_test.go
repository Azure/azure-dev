// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS kubelet kubeletidentity crnative AGENTHOSTING
// cspell:ignore INCLUDEACR KUBELETOBJECTID

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
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
	mu                sync.Mutex
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
	f.mu.Lock()
	defer f.mu.Unlock()
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
			p, fake := suppliedProvider(t, 7)
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

			err = p.prepareBYO(t.Context(), source)
			if test.enabled {
				require.NoError(t, err)
				assert.ElementsMatch(t, []string{
					offlineClusterID,
					"/subscriptions/workload-sub/providers/Microsoft.Authorization/roleDefinitions",
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
	require.Len(t, fake.requests, 4)
	assert.ElementsMatch(t, []string{
		"GET " + offlineClusterID,
		"GET /subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions",
	}, fake.requests[:2])
	assert.Equal(t, "GET /subscriptions/target-sub/providers/Microsoft.Resources/deployments/"+p.deploymentName(),
		fake.requests[2])
	assert.Equal(t, "PUT /subscriptions/target-sub/providers/Microsoft.Resources/deployments/"+p.deploymentName(),
		fake.requests[3])
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

func TestBYORegistryValidationBeforeLifecycleWrites(t *testing.T) {
	const registryID = "/subscriptions/target-sub/resourceGroups/shared/providers/" +
		"Microsoft.ContainerRegistry/registries/registry"
	for _, test := range []struct {
		name, expectedError   string
		abac, remote, invalid bool
	}{
		{name: "ABAC remote", abac: true, remote: true, expectedError: "does not authenticate"},
		{name: "ABAC local", abac: true, expectedError: "registry reuse is not supported with agentHosting"},
		{name: "classic remote", remote: true, expectedError: "registry reuse is not supported with agentHosting"},
		{name: "invalid hosting", invalid: true, expectedError: "registry reuse is not supported with agentHosting"},
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
				p.synthResult.Hosting.ClusterResourceID += "-other"
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
			assert.Empty(t, fake.requests)
			assert.Zero(t, fake.writes)
			assert.Empty(t, env.set)
			assert.Empty(t, env.config)
			assert.Nil(t, p.acrPending)
			assert.Len(t, p.acrPromptedEnv, 2)
		})
	}
}

func TestBYORegistryPreviewRejectsReuseWithoutWrites(t *testing.T) {
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
			require.ErrorIs(t, err, synthesis.ErrACRReuseAgentHosting)
			assert.Zero(t, fake.previews)
			assert.Zero(t, fake.writes)
			assert.Empty(t, fake.requests)
			assert.Empty(t, env.set)
			assert.Empty(t, env.config)
			assert.Nil(t, p.acrPending)
			assert.Empty(t, fake.parameters)
		})
	}
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
	assert.Len(t, fake.requests, 8)
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
	assert.Equal(t, map[string]any{"value": offlineKubeletObjectID}, fake.parameters["kubeletObjectId"])
	assert.Contains(t, fake.requests, "GET "+offlineClusterID)
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
				expectedRequests := []string{}
				if operation == "deploy" {
					expectedRequests = append(expectedRequests,
						"GET /subscriptions/target-sub/providers/Microsoft.Resources/deployments/"+p.deploymentName())
				}
				expectedRequests = append(expectedRequests,
					method+" /subscriptions/target-sub/providers/Microsoft.Resources/deployments/"+
						p.deploymentName()+suffix)
				require.Len(t, fake.requests, len(expectedRequests)+2)
				assert.ElementsMatch(t, []string{
					"GET " + offlineClusterID,
					"GET /subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions",
				}, fake.requests[:2])
				assert.Equal(t, expectedRequests, fake.requests[2:])
			})
		}
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
	p, fake := suppliedProvider(t, 7)
	source, err := p.resolveTemplate(t.Context(), func(string) {})
	require.NoError(t, err)
	definitions, ok := source.armTemplate["parameters"].(map[string]any)
	require.True(t, ok)
	for _, name := range []string{
		"kubeletObjectId", "federatedIdentityRoleDefinitionId", "hostingPrincipalId", "workloadPrincipalId",
	} {
		delete(definitions, name)
	}
	require.NoError(t, p.prepareBYO(t.Context(), source))
	assert.NotContains(t, source.parameters, "kubeletObjectId")
	assert.NotContains(t, source.parameters, "federatedIdentityRoleDefinitionId")
	assert.NotContains(t, source.parameters, "hostingPrincipalId")
	assert.NotContains(t, source.parameters, "workloadPrincipalId")
	assert.Equal(t, []string{offlineClusterID}, fake.reads, "the actual kubelet identity is always checked")
}

func TestBYORejectsExternalRegistryBeforeAuthentication(t *testing.T) {
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
			p.credential = nil
			require.ErrorIs(t, p.prepareBYO(t.Context(), source), synthesis.ErrACRReuseAgentHosting)
			assert.Empty(t, fake.requests)
		})
	}
}

func TestBYOPrepareValidatesKubeletIdentityRegardlessOfACR(t *testing.T) {
	for _, includeAcr := range []bool{false, true} {
		for _, supplied := range []bool{false, true} {
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
				t.Run(fmt.Sprintf("includeAcr=%t/supplied=%t/%s", includeAcr, supplied, test.name), func(t *testing.T) {
					p, fake := offlineBYOProvider(t)
					fake.clusterProperties = test.properties
					source, err := p.resolveTemplate(t.Context(), func(string) {})
					require.NoError(t, err)
					source.parameters["includeAcr"] = map[string]any{"value": includeAcr}
					if supplied {
						source.parameters["kubeletObjectId"] = map[string]any{"value": suppliedHostingPrincipal}
					}
					require.ErrorContains(t, p.prepareBYO(t.Context(), source), test.expected)
					assert.Contains(t, fake.requests, "GET "+offlineClusterID)
					assert.Subset(t, []string{
						"GET " + offlineClusterID,
						"GET /subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions",
					}, fake.requests)
				})
			}
		}
	}
}

func TestBYOPreparePreservesSuppliedKubeletRegardlessOfACR(t *testing.T) {
	for _, includeAcr := range []bool{false, true} {
		for _, test := range []struct {
			name      string
			value     any
			duplicate bool
		}{
			{"empty", "", false},
			{"whitespace", " ", false},
			{"non-string", 1, false},
			{"null", nil, false},
			{"reference", map[string]any{"reference": map[string]any{"secretName": "kubelet"}}, false},
			{"duplicate", offlineKubeletObjectID, true},
			{"different", "22222222-2222-4222-8222-222222222222", false},
			{"matching", strings.ToUpper(offlineKubeletObjectID), false},
		} {
			t.Run(fmt.Sprintf("includeAcr=%t/%s", includeAcr, test.name), func(t *testing.T) {
				p, fake := offlineBYOProvider(t)
				source, err := p.resolveTemplate(t.Context(), func(string) {})
				require.NoError(t, err)
				delete(source.parameters, "includeAcr")
				source.parameters["INCLUDEACR"] = map[string]any{"value": includeAcr}
				parameter := any(map[string]any{"value": test.value})
				if test.name == "reference" {
					parameter = test.value
				}
				source.parameters["kubeletObjectId"] = parameter
				if test.duplicate {
					source.parameters["KUBELETOBJECTID"] = map[string]any{"value": test.value}
				}
				require.NoError(t, p.prepareBYO(t.Context(), source))
				assert.ElementsMatch(t, []string{
					"GET " + offlineClusterID,
					"GET /subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions",
				}, fake.requests)
				assert.Equal(t, parameter, source.parameters["kubeletObjectId"])
				if test.duplicate {
					assert.Equal(t, map[string]any{"value": test.value}, source.parameters["KUBELETOBJECTID"])
				}
				assert.Equal(t, map[string]any{"value": includeAcr}, source.parameters["INCLUDEACR"])
			})
		}
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
			require.ErrorContains(t, p.prepareBYO(t.Context(), source), test.expected)
			assert.Empty(t, fake.requests)
		})
	}
}
