// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/require"
)

func TestExistingProjectEffectiveParametersValidateBeforeMutation(t *testing.T) {
	const (
		projectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/resource-account/projects/project"
		endpoint = "https://custom-dns.services.ai.azure.com/api/projects/project"
	)
	for _, tt := range []struct {
		name, projectID, endpoint, wantError string
		defaults, upperCase                  bool
	}{
		{name: "matching custom domain", projectID: projectID, endpoint: endpoint},
		{name: "case-insensitive parameter names", projectID: projectID, endpoint: endpoint, upperCase: true},
		{name: "mismatched parameter endpoint", projectID: projectID,
			endpoint:  "https://other-dns.services.ai.azure.com/api/projects/project",
			wantError: "projectEndpoint must match the existing Foundry project selected during initialization"},
		{name: "omitted parameter retains host value", projectID: projectID, defaults: true,
			endpoint: "https://other-dns.services.ai.azure.com/api/projects/project"},
		{name: "overridden ARM identity", projectID: strings.Replace(projectID, "resource-account", "another-account", 1),
			endpoint:  endpoint,
			wantError: "projectResourceId must match the existing Foundry project selected during initialization"},
		{name: "mutually matching override must still match initialized identity",
			projectID: strings.Replace(projectID, "resource-account", "another-account", 1),
			endpoint:  "https://another-dns.services.ai.azure.com/api/projects/project",
			wantError: "projectResourceId must match the existing Foundry project selected during initialization"},
		{name: "matching identity with endpoint normalization",
			projectID: projectID,
			endpoint:  " HTTPS://CUSTOM-DNS.services.ai.azure.com/projects/%70roject/// "},
		// #nosec G101 -- synthetic URL for credential rejection.
		{name: "credential-bearing endpoint", projectID: projectID,
			endpoint:  "https://endpoint-user:endpoint-password@custom-dns.services.ai.azure.com/api/projects/project",
			wantError: "must not contain credentials"},
		{name: "invalid ARM identity", projectID: "invalid", endpoint: endpoint,
			wantError: "not a Foundry project resource ID"},
	} {
		for _, operation := range []string{"deploy", "preview"} {
			t.Run(tt.name+"/"+operation, func(t *testing.T) {
				template, err := synthesis.ExistingProjectARMTemplate()
				require.NoError(t, err)
				var compiled map[string]any
				require.NoError(t, json.Unmarshal(template, &compiled))
				parameters := map[string]any{
					"projectResourceId": map[string]any{"value": tt.projectID},
					"projectEndpoint":   map[string]any{"value": tt.endpoint},
				}
				if tt.upperCase {
					parameters = map[string]any{
						strings.ToUpper("projectResourceId"): parameters["projectResourceId"],
						strings.ToUpper("projectEndpoint"):   parameters["projectEndpoint"],
					}
				}
				if tt.defaults {
					declarations, ok := compiled["parameters"].(map[string]any)
					require.True(t, ok)
					declarations["projectEndpoint"] = map[string]any{"type": "string", "defaultValue": tt.endpoint}
					delete(parameters, "projectEndpoint")
				}
				root := t.TempDir()
				infraDir := filepath.Join(root, onDiskInfraDir)
				require.NoError(t, os.MkdirAll(infraDir, 0o750))
				require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), []byte("// template\n"), 0o600))
				template, err = json.Marshal(compiled)
				require.NoError(t, err)
				parameterFile, err := json.Marshal(map[string]any{"parameters": parameters})
				require.NoError(t, err)
				parameterPath := filepath.Join(infraDir, onDiskParamsFile)
				require.NoError(t, os.WriteFile(parameterPath, parameterFile, 0o600))
				env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
					"AZURE_AI_PROJECT_ID": projectID, "FOUNDRY_PROJECT_ENDPOINT": endpoint,
				}}
				before := maps.Clone(env.get)
				p := &FoundryProvisioningProvider{
					subID: "sub", location: "eastus", envName: "test",
					existingProjectID: projectID, brownfieldEndpoint: endpoint,
					projectPath:      root,
					bicepCliInstance: &stubCompiler{buildResult: bicep.BuildResult{Compiled: string(template)}},
					azdClient:        newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{}),
					credential:       acrTestCredential{},
				}
				var reads, writes []string
				p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
					Retry: policy.RetryOptions{MaxRetries: -1},
					Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
						if request.Method != http.MethodGet {
							writes = append(writes, request.URL.Path)
							return acrTestHTTPReply(t, request, http.StatusBadRequest, map[string]any{
								"error": map[string]any{"code": "TestDeploymentReached"},
							})
						}
						reads = append(reads, request.URL.Path)
						resolvedEndpoint := endpoint
						subdomain := "custom-dns"
						if strings.Contains(request.URL.Path, "/accounts/another-account") {
							resolvedEndpoint = "https://another-dns.services.ai.azure.com/api/projects/project"
							subdomain = "another-dns"
						}
						if strings.HasSuffix(request.URL.Path, "/projects/project") {
							return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
								"properties": map[string]any{
									"endpoints": map[string]any{"AI Foundry API": resolvedEndpoint},
								},
							})
						}
						if strings.Contains(request.URL.Path, "/accounts/") {
							return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
								"properties": map[string]any{"customSubDomainName": subdomain},
							})
						}
						return acrTestHTTPReply(t, request, http.StatusNotFound, map[string]any{
							"error": map[string]any{"code": "DeploymentNotFound"},
						})
					}),
				}}
				switch operation {
				case "deploy":
					_, err = p.Deploy(t.Context(), func(string) {})
				case "preview":
					_, err = p.Preview(t.Context(), func(string) {})
				}
				require.Error(t, err)
				require.NotContains(t, err.Error(), "endpoint-password")
				if tt.wantError != "" {
					require.ErrorContains(t, err, tt.wantError)
					require.Empty(t, writes, "identity validation must precede every remote mutation")
				} else {
					require.ErrorContains(t, err, "TestDeploymentReached")
					require.NotEmpty(t, writes, "a verified custom domain must reach ARM deployment")
				}
				if strings.Contains(tt.projectID, "/accounts/another-account") &&
					!strings.Contains(tt.wantError, "must match the existing Foundry project selected during initialization") {
					require.Contains(t, reads, tt.projectID, "validate the effective ID rather than the environment default")
				}
				require.Equal(t, before, env.get)
				// #nosec G304 -- path is under the test-owned root.
				saved, readErr := os.ReadFile(parameterPath)
				require.NoError(t, readErr)
				require.Equal(t, parameterFile, saved)
			})
		}
	}
}

func TestValidateProjectIdentityClassifiesLookupFailures(t *testing.T) {
	const (
		projectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/resource-account/projects/project"
		endpoint = "https://custom-dns.services.ai.azure.com/api/projects/project"
	)
	t.Run("arm response error becomes service error", func(t *testing.T) {
		p := &FoundryProvisioningProvider{
			credential: acrTestCredential{},
			armClientOptions: &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
					if request.URL.Path == projectID {
						return acrTestHTTPReply(t, request, http.StatusForbidden, map[string]any{
							"error": map[string]any{"code": "AuthorizationFailed", "message": "access denied"},
						})
					}
					return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
						"properties": map[string]any{"customSubDomainName": "custom-dns"},
					})
				}),
			}},
		}
		err := p.validateProjectIdentity(t.Context(), projectID, endpoint)
		require.Error(t, err)
		serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
		require.True(t, ok)
		require.Equal(t, http.StatusForbidden, serviceErr.StatusCode)
		require.Contains(t, serviceErr.Message, "AuthorizationFailed")
	})

	t.Run("invalid metadata becomes validation error", func(t *testing.T) {
		p := &FoundryProvisioningProvider{
			credential: acrTestCredential{},
			armClientOptions: &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
					if request.URL.Path == projectID {
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
							"properties": map[string]any{"endpoints": map[string]any{
								"AI Foundry API": "https://example.com/api/projects/project",
							}},
						})
					}
					return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
						"properties": map[string]any{"customSubDomainName": "custom-dns"},
					})
				}),
			}},
		}
		err := p.validateProjectIdentity(t.Context(), projectID, endpoint)
		require.Error(t, err)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		require.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
	})
}

func TestExistingProjectIdentityParametersRejectAmbiguity(t *testing.T) {
	for _, tt := range []struct {
		name, wantError string
		parameters      map[string]any
	}{
		{name: "case aliases", wantError: "differ only by case", parameters: map[string]any{
			"projectEndpoint":                  map[string]any{"value": "endpoint"},
			strings.ToUpper("projectEndpoint"): map[string]any{"value": "another"},
		}},
		{name: "reference envelope", wantError: "value envelope", parameters: map[string]any{
			"projectEndpoint": map[string]any{"reference": map[string]any{"secretName": "endpoint"}},
		}},
		{name: "empty endpoint", wantError: "non-empty literal string", parameters: map[string]any{
			"projectEndpoint": map[string]any{"value": ""},
		}},
		{name: "ARM expression", wantError: "non-empty literal string", parameters: map[string]any{
			"projectEndpoint": map[string]any{"value": "[parameters('endpoint')]"},
		}},
		{name: "non-string endpoint", wantError: "non-empty literal string", parameters: map[string]any{
			"projectEndpoint": map[string]any{"value": 42},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := projectIdentityParameter(&templateSource{parameters: tt.parameters}, "projectEndpoint")
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestExistingProjectCustomSubdomainValidation(t *testing.T) {
	const (
		projectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/resource-account/projects/project"
		endpoint = "https://custom-dns.services.ai.azure.com/api/projects/project"
	)
	for _, tt := range []struct {
		name, configured string
	}{
		{name: "verified custom subdomain", configured: endpoint},
		{name: "another account with the same project name",
			configured: "https://other-account.services.ai.azure.com/api/projects/project"},
		{name: "stale resource-name endpoint",
			configured: "https://resource-account.services.ai.azure.com/api/projects/project"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			raw := "services:\n  project:\n    host: azure.ai.project\n    endpoint: " + tt.configured + "\n" +
				"    deployments:\n      - name: chat\n" +
				"        model: {name: gpt-4.1, format: OpenAI, version: '2025-04-14'}\n" +
				"        sku: {name: GlobalStandard, capacity: 1}\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(raw), 0600))
			env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
				envKeySubscriptionID: "sub", envKeyLocation: "eastus", envKeyFoundryRG: "supporting",
				"AZURE_AI_PROJECT_ID": projectID, "FOUNDRY_PROJECT_ENDPOINT": tt.configured,
			}}
			before := maps.Clone(env.get)
			client := newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
			p := &FoundryProvisioningProvider{azdClient: client}
			require.NoError(t, p.Initialize(t.Context(), root,
				&azdext.ProvisioningOptions{Provider: FoundryProviderName}))
			require.Nil(t, p.credential, "Initialize must remain offline")
			require.Equal(t, before, env.get)
			require.Equal(t, projectID, p.existingProjectID)
			source, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			parameters := source.parameters
			projectParameter, ok := parameters["projectResourceId"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, projectID, projectParameter["value"])
			endpointParameter, ok := parameters["projectEndpoint"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, tt.configured, endpointParameter["value"])

			var requests []string
			deployed := false
			outputs := map[string]any{}
			for key, value := range map[string]string{
				"AZURE_AI_PROJECT_ID": projectID, "FOUNDRY_PROJECT_ENDPOINT": endpoint,
				"AZURE_AI_ACCOUNT_NAME": "resource-account", "AZURE_AI_PROJECT_NAME": "project",
				"AZURE_OPENAI_ENDPOINT": "https://custom-dns.openai.azure.com/",
			} {
				outputs[key] = map[string]any{"type": "String", "value": value}
			}
			p.credential = acrTestCredential{}
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
					requests = append(requests, request.Method+" "+request.URL.Path)
					switch request.URL.Path {
					case projectID:
						require.Equal(t, http.MethodGet, request.Method)
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
							"properties": map[string]any{"endpoints": map[string]any{"AI Foundry API": endpoint}},
						})
					case strings.TrimSuffix(projectID, "/projects/project"):
						require.Equal(t, http.MethodGet, request.Method)
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
							"properties": map[string]any{"customSubDomainName": "custom-dns"},
						})
					case "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName():
						require.Equal(t, endpoint, tt.configured, "mismatched identities must not reach deployments")
						if request.Method == http.MethodGet && !deployed {
							return acrTestHTTPReply(t, request, http.StatusNotFound, map[string]any{
								"error": map[string]any{"code": "DeploymentNotFound"},
							})
						}
						if request.Method == http.MethodPut {
							var submission struct {
								Properties struct {
									Parameters struct {
										ID       struct{ Value string } `json:"projectResourceId"`
										Endpoint struct{ Value string } `json:"projectEndpoint"`
									}
								}
							}
							require.NoError(t, json.NewDecoder(request.Body).Decode(&submission))
							require.Equal(t, projectID, submission.Properties.Parameters.ID.Value)
							require.Equal(t, endpoint, submission.Properties.Parameters.Endpoint.Value)
							deployed = true
						} else {
							require.Equal(t, http.MethodGet, request.Method)
						}
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
							"id": request.URL.Path, "properties": map[string]any{
								"provisioningState": "Succeeded", "outputs": outputs,
							},
						})
					default:
						t.Fatalf("unexpected request before identity validation: %s", request.URL.Path)
						return nil, context.Canceled
					}
				}),
			}}
			if tt.configured == endpoint {
				result, err := p.Deploy(t.Context(), func(string) {})
				require.NoError(t, err)
				require.True(t, deployed)
				state, err := p.State(t.Context(), &azdext.ProvisioningStateOptions{})
				require.NoError(t, err)
				for key, expected := range outputs {
					output, ok := expected.(map[string]any)
					require.True(t, ok)
					require.Contains(t, result.Deployment.Outputs, key)
					require.Equal(t, output["value"], result.Deployment.Outputs[key].Value)
					require.Contains(t, state.State.Outputs, key)
					require.Equal(t, output["value"], state.State.Outputs[key].Value)
				}
			} else {
				for _, operation := range []string{"deploy", "preview", "state"} {
					t.Run(operation, func(t *testing.T) {
						var err error
						switch operation {
						case "deploy":
							_, err = p.Deploy(t.Context(), func(string) {})
						case "preview":
							_, err = p.Preview(t.Context(), func(string) {})
						case "state":
							_, err = p.State(t.Context(), &azdext.ProvisioningStateOptions{})
						}
						require.ErrorContains(t, err, "does not identify the project")
					})
				}
			}
			require.NotEmpty(t, requests)
			if tt.configured != endpoint {
				for _, request := range requests {
					require.True(t, strings.HasPrefix(request, "GET "), request)
				}
			}
			require.Equal(t, before, env.get)
			// #nosec G304 -- path is under the test-owned root.
			saved, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, raw, string(saved))
		})
	}
}

func TestExistingProjectDestroyVerifiesIdentity(t *testing.T) {
	const (
		projectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/resource-account/projects/project"
		endpoint = "https://custom-dns.services.ai.azure.com/api/projects/project"
		groupID  = "/subscriptions/sub/resourceGroups/supporting"
		acrID    = groupID + "/providers/Microsoft.ContainerRegistry/registries/registry"
	)
	for _, tt := range []struct {
		name, configured, wantError  string
		projectStatus, accountStatus int
		connectionMissing            bool
	}{
		{name: "custom subdomain", configured: endpoint},
		{name: "connection already deleted", configured: endpoint, connectionMissing: true},
		{name: "project already deleted", configured: endpoint, projectStatus: http.StatusNotFound},
		{name: "account already deleted", configured: endpoint, accountStatus: http.StatusNotFound},
		{name: "other account with the same project name",
			configured: "https://other-account.services.ai.azure.com/api/projects/project",
			wantError:  "does not identify the project"},
		{name: "project access denied", configured: endpoint, projectStatus: http.StatusForbidden,
			wantError: "ResourceUnavailable"},
		{name: "account access denied with valid project metadata", configured: endpoint, accountStatus: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			raw := "services:\n  project:\n    host: azure.ai.project\n    endpoint: " + tt.configured + "\n" +
				"  agent:\n    host: azure.ai.agent\n    uses: [project]\n    docker:\n      path: Dockerfile\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(raw), 0o600))
			env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
				envKeySubscriptionID: "sub", envKeyLocation: "eastus", envKeyFoundryRG: "supporting",
				envKeyFoundryRGOwner: groupID, "AZURE_AI_PROJECT_ID": projectID,
				"FOUNDRY_PROJECT_ENDPOINT": tt.configured, "AZD_FOUNDRY_ACR_MODE": "create",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": acrID, "AZURE_CONTAINER_REGISTRY_ENDPOINT": "registry.azurecr.io",
				"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": "registry-conn",
			}}
			before := maps.Clone(env.get)
			p := &FoundryProvisioningProvider{
				azdClient: newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{}),
			}
			require.NoError(t, p.Initialize(t.Context(), root,
				&azdext.ProvisioningOptions{Provider: FoundryProviderName}))
			require.Nil(t, p.credential, "Initialize must remain offline")
			require.Equal(t, "create", p.existingAcrMode)
			connectionID := projectID + "/connections/registry-conn"
			accountID := strings.TrimSuffix(projectID, "/projects/project")
			deploymentID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
			var requests []string
			var deleted []string
			groupDeleted := false
			connectionDeleted := tt.connectionMissing
			p.credential = acrTestCredential{}
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
					path := request.URL.Path
					requests = append(requests, request.Method+" "+path)
					if request.Method == http.MethodDelete {
						deleted = append(deleted, path)
					}
					reply := func(code int, value any) (*http.Response, error) {
						return acrTestHTTPReply(t, request, code, value)
					}
					switch path {
					case projectID, accountID:
						require.Equal(t, http.MethodGet, request.Method)
						code := tt.projectStatus
						body := map[string]any{
							"properties": map[string]any{"endpoints": map[string]any{"AI Foundry API": endpoint}},
						}
						if path == accountID {
							code = tt.accountStatus
							body = map[string]any{"properties": map[string]any{"customSubDomainName": "custom-dns"}}
						}
						if code != 0 {
							return reply(code, map[string]any{
								"error": map[string]any{"code": "ResourceUnavailable"},
							})
						}
						return reply(http.StatusOK, body)
					case connectionID:
						require.Empty(t, tt.wantError, "invalid identity must not reach project connections")
						if request.Method == http.MethodDelete {
							connectionDeleted = true
							return reply(http.StatusNoContent, nil)
						}
						require.Equal(t, http.MethodGet, request.Method)
						if connectionDeleted {
							return reply(http.StatusNotFound, nil)
						}
						return reply(http.StatusOK, map[string]any{"properties": map[string]any{
							"category": "ContainerRegistry", "authType": "ManagedIdentity",
							"target": "registry.azurecr.io", "metadata": map[string]any{"ResourceId": acrID},
						}})
					case groupID, strings.ToLower(groupID):
						if request.Method == http.MethodDelete {
							groupDeleted = true
							return reply(http.StatusOK, nil)
						}
						require.Equal(t, http.MethodGet, request.Method)
						if groupDeleted {
							return reply(http.StatusNotFound, nil)
						}
						return reply(http.StatusOK, map[string]any{"tags": map[string]any{"azd-env-name": "test"}})
					case groupID + "/providers/Microsoft.CognitiveServices/accounts":
						require.Equal(t, http.MethodGet, request.Method)
						return reply(http.StatusOK, map[string]any{"value": []any{}})
					case deploymentID:
						require.Equal(t, http.MethodDelete, request.Method)
						require.True(t, groupDeleted)
						return reply(http.StatusNoContent, nil)
					default:
						t.Fatalf("unexpected teardown request: %s %s", request.Method, path)
						return nil, context.Canceled
					}
				}),
			}}
			result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			require.Contains(t, requests, "GET "+projectID)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				require.Nil(t, result)
				require.Empty(t, deleted)
				require.NotContains(t, requests, "GET "+connectionID)
			} else {
				require.NoError(t, err)
				require.Contains(t, deleted, strings.ToLower(groupID))
				require.Contains(t, deleted, deploymentID)
				require.NotContains(t, result.InvalidatedEnvKeys, "AZURE_AI_PROJECT_ID")
				require.NotContains(t, result.InvalidatedEnvKeys, "FOUNDRY_PROJECT_ENDPOINT")
				if tt.projectStatus == http.StatusNotFound || tt.accountStatus == http.StatusNotFound {
					require.NotContains(t, requests, "GET "+connectionID)
					require.NotContains(t, deleted, connectionID)
				} else if tt.connectionMissing {
					require.Contains(t, requests, "GET "+connectionID)
					require.NotContains(t, deleted, connectionID)
				} else {
					require.Contains(t, deleted, connectionID)
				}
				_, err = p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
				require.NoError(t, err)
			}
			require.Equal(t, before, env.get)
			require.Empty(t, env.set)
			require.Zero(t, env.configSetN)
			require.Zero(t, env.configUnsetN)
			//nolint:gosec // Test-owned path.
			actual, readErr := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, readErr)
			require.Equal(t, raw, string(actual))
		})
	}
}
