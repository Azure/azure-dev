// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const offlineClusterID = "/subscriptions/cluster-sub/resourceGroups/aks-rg/providers/" +
	"Microsoft.ContainerService/managedClusters/aks"
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

func (byoTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "offline-only", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type byoARMFake struct {
	t          *testing.T
	provider   *FoundryProvisioningProvider
	requests   []string
	parameters map[string]any
	template   map[string]any
	writes     int
	previews   int
	failWrite  bool
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
		return f.response(request, 200, map[string]any{
			"properties": map[string]any{"identityProfile": map[string]any{
				"kubeletidentity": map[string]any{"objectId": "11111111-1111-4111-8111-111111111111"},
			}},
		}), nil
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
	if request.Method == http.MethodGet && request.URL.Path == "/subscriptions/target-sub/resourcegroups/target-rg" {
		return f.response(request, 200, map[string]any{"location": "eastus"}), nil
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
	if request.Method == http.MethodPost && request.URL.Path == path+"/whatIf" {
		f.previews++
		return f.response(request, 200, map[string]any{
			"status": "Succeeded",
			"properties": map[string]any{"changes": []any{map[string]any{
				"resourceId": offlineRegistryID, "changeType": "Create",
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
			"azurE_CONTAINER_REGISTRY_RESOURCE_ID": output(offlineRegistryID),
			"azurE_CONTAINER_REGISTRY_ENDPOINT":    output("crnative.azurecr.io"),
			"azurE_AI_PROJECT_ACR_CONNECTION_NAME": output("crnative-conn"),
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
	fake := &byoARMFake{t: t, provider: p}
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
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": offlineRegistryID,
		"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "crnative.azurecr.io",
		"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": "crnative-conn",
		"AZURE_TENANT_ID":                      "user-access-tenant",
	} {
		require.Contains(t, result.Deployment.Outputs, name)
		assert.Equal(t, value, result.Deployment.Outputs[name].Value)
	}
	assert.Equal(t, 1, fake.writes)
	assert.Equal(t, []string{
		"GET " + offlineClusterID,
		"GET /subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions",
		"GET /subscriptions/target-sub/resourcegroups/target-rg",
		"PUT /subscriptions/target-sub/providers/Microsoft.Resources/deployments/" + p.deploymentName(),
	}, fake.requests)
	assert.Empty(t, server.set)
	assert.Empty(t, compiler.buildCalls)
	assert.Empty(t, compiler.buildParamCalls, "embedded provisioning must not invoke a Bicep naming probe")
	assert.Contains(t, fake.parameters, "kubeletObjectId")
	assert.Contains(t, fake.parameters, "federatedIdentityRoleDefinitionId")
}

func TestBYOPreviewReturnsARMWhatIfChangesWithoutResourceWrites(t *testing.T) {
	p, fake := offlineBYOProvider(t)
	result, err := p.Preview(t.Context(), func(string) {})
	require.NoError(t, err)
	require.NotNil(t, result.Preview)
	require.Len(t, result.Preview.Changes, 1)
	assert.Equal(t, offlineRegistryID, result.Preview.Changes[0].ResourceId)
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
	assert.Len(t, fake.requests, 8)
	assert.Empty(t, p.virtualEnv)
}

func TestBYOAllowsCustomizedOnDiskTemplateAndParameters(t *testing.T) {
	p, fake := offlineBYOProvider(t)
	p.armTemplate["contentVersion"] = "2.0.0.0"
	p.onDiskSource = &templateSource{
		mode: templateModeBicep, armTemplate: p.armTemplate,
		parameters: map[string]any{
			"resourceTokenSalt": map[string]any{"value": "custom"},
			"tags":              map[string]any{"value": map[string]string{"team": "test"}},
		},
	}
	_, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, "2.0.0.0", fake.template["contentVersion"])
	assert.Equal(t, map[string]any{"value": "custom"}, fake.parameters["resourceTokenSalt"])
	p.onDiskSource.parameters["resourceTokenSalt"] = map[string]any{"value": "updated"}
	_, err = p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"value": "updated"}, fake.parameters["resourceTokenSalt"])
	assert.Equal(t, 2, fake.writes)
}

func TestNativeFoundryDeployKeepsStandardOutputsWithoutAKSReads(t *testing.T) {
	p, fake := offlineBYOProvider(t)
	t.Setenv(synthesis.BYOAKSEnvVar, "")
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
	require.NoError(t, p.prepareBYO(t.Context(), source))
	assert.NotContains(t, source.parameters, "kubeletObjectId")
	assert.NotContains(t, source.parameters, "federatedIdentityRoleDefinitionId")
	assert.Len(t, fake.requests, 3, "preparation only reads host bindings and the resource group")
}

func TestBYORejectsLocalConflictsBeforeAuthentication(t *testing.T) {
	for _, test := range []struct {
		key, expected string
		value         any
	}{
		{"includeAcr", "requires includeAcr=true", false},
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

func TestBYOInitializeRejectsInvalidInputsBeforePromptsOrPersistence(t *testing.T) {
	for _, onDisk := range []bool{false, true} {
		root := t.TempDir()
		config := "services:\n  project:\n    host: azure.ai.project\n" +
			"    agentHosting: {clusterResourceId: " + offlineClusterID + ", storageAccountResourceId: ''}\n"
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
		require.ErrorContains(t, err, "storageAccountResourceId: unsupported property")
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

func TestBYOFeatureProcessOptIn(t *testing.T) {
	t.Setenv("AZD_ALPHA_ENABLE_ALL", "true")
	t.Setenv("AZD_ALPHA_ENABLE_AGENTS_VOICE", "true")
	t.Setenv("AZD_FOUNDRY_EXT_ENABLE_AGENTS_BYO_AKS", "true")
	for _, value := range []string{
		"", "0", "false", "FALSE", "False", "f", "F", "invalid", "on", "yes", "TrUe", " true ",
		"1", "t", "T", "true", "TRUE", "True",
	} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(synthesis.BYOAKSEnvVar, value)
			if value == "" {
				require.NoError(t, os.Unsetenv(synthesis.BYOAKSEnvVar))
			}
			err := requireBYOAKSFeature()
			switch value {
			case "1", "t", "T", "true", "TRUE", "True":
				require.NoError(t, err)
			default:
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "%v", err)
				assert.Equal(t, exterrors.CodeFeatureNotEnabled, local.Code)
				assert.Equal(t, azdext.LocalErrorCategoryCompatibility, local.Category)
				assert.Contains(t, local.Message, synthesis.BYOAKSFeatureID)
				assert.Contains(t, local.Suggestion, synthesis.BYOAKSEnvVar+"=true in your shell or CI")
			}
		})
	}
}

func TestBYOInitializeRequiresOptInBeforePromptsOrPersistence(t *testing.T) {
	for _, onDisk := range []bool{false, true} {
		for _, ref := range []bool{false, true} {
			for _, value := range []string{"", "false", "invalid", "true"} {
				name := map[bool]string{false: "embedded", true: "on-disk"}[onDisk] + "/" +
					map[bool]string{false: "inline", true: "ref"}[ref] + "/" + value
				t.Run(name, func(t *testing.T) {
					t.Setenv(synthesis.BYOAKSEnvVar, value)
					root := t.TempDir()
					config := offlineBYOAzureYAML
					if ref {
						body := strings.TrimPrefix(config, "\nservices:\n  project:\n")
						require.NoError(t, os.WriteFile(filepath.Join(root, "project.yaml"), []byte(body), 0o600))
						config = "services:\n  project:\n    host: azure.ai.project\n    $ref: project.yaml\n"
					}
					path := filepath.Join(root, "azure.yaml")
					require.NoError(t, os.WriteFile(path, []byte(config), 0o600))
					if onDisk {
						require.NoError(t, os.Mkdir(filepath.Join(root, "infra"), 0o750))
						require.NoError(t, os.WriteFile(
							filepath.Join(root, "infra", "main.bicep"), []byte("// generated"), 0o600))
					}
					env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
						envKeySubscriptionID: "target-sub", envKeyLocation: "westus",
						synthesis.BYOAKSEnvVar: "true",
					}}
					prompt := &resolveEnvStubPromptServer{}
					p := &FoundryProvisioningProvider{azdClient: newResolveEnvTestClient(t, env, prompt)}
					err := p.Initialize(t.Context(), root, &azdext.ProvisioningOptions{Provider: FoundryProviderName})
					if value == "true" {
						require.NoError(t, err)
						require.NotNil(t, p.synthResult.Hosting)
						assert.Equal(t, offlineClusterID, p.synthResult.Hosting.ClusterResourceID)
					} else {
						local, ok := errors.AsType[*azdext.LocalError](err)
						require.True(t, ok, "%v", err)
						assert.Equal(t, exterrors.CodeFeatureNotEnabled, local.Code)
						assert.Empty(t, env.set)
					}
					assert.Zero(t, prompt.subscriptionN)
					assert.Zero(t, prompt.locationN)
					assert.Nil(t, p.credential)
					//nolint:gosec // Test-owned azure.yaml under t.TempDir.
					raw, err := os.ReadFile(path)
					require.NoError(t, err)
					assert.Equal(t, config, string(raw))
				})
			}
		}
	}
}

func TestBYODeployAndPreviewRejectDisabledFeatureBeforeAuthentication(t *testing.T) {
	for _, operation := range []string{"deploy", "preview"} {
		t.Run(operation, func(t *testing.T) {
			p, fake := offlineBYOProvider(t)
			t.Setenv(synthesis.BYOAKSEnvVar, "false")
			p.credential = nil
			var err error
			if operation == "deploy" {
				_, err = p.Deploy(t.Context(), func(string) {})
			} else {
				_, err = p.Preview(t.Context(), func(string) {})
			}
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "%v", err)
			assert.Equal(t, exterrors.CodeFeatureNotEnabled, local.Code)
			assert.Nil(t, p.credential)
			assert.Empty(t, fake.requests)
			assert.Empty(t, p.virtualEnv)
		})
	}
}
