// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestACRRemoteBuildPreflight(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, tt := range []struct {
		name, service, endpoint, provider string
		classic, canonical, noSelector    bool
		managed                           bool
		reject                            bool
	}{
		{name: "remote ABAC", service: "docker: {remoteBuild: true}", reject: true},
		{name: "empty endpoint variable", service: "docker: {remoteBuild: true}",
			endpoint: "${FOUNDRY_PROJECT_ENDPOINT}", reject: true},
		{name: "expanded existing endpoint", service: "docker: {remoteBuild: true}",
			endpoint: "${FOUNDRY_PROJECT_ENDPOINT}"},
		{name: "generated remote default", service: "kind: hosted\ndocker: {remoteBuild: true}", reject: true},
		{name: "canonical only", service: "docker: {remoteBuild: true}", canonical: true, reject: true},
		{name: "legacy managed registry", service: "docker: {remoteBuild: true}", managed: true},
		{name: "canonical managed registry", service: "docker: {remoteBuild: true}", managed: true, canonical: true},
		{name: "classic remote", service: "docker: {remoteBuild: true}", classic: true},
		{name: "explicit local", service: "docker: {remoteBuild: false}"},
		{name: "local overrides nested remote", service: "docker: {remoteBuild: false}\n" +
			"config:\n  docker: {remoteBuild: true}"},
		{name: "local overrides referenced remote", service: "docker: {remoteBuild: false}\n" +
			"config:\n  $ref: agent.yaml"},
		{name: "referenced remote", service: "config:\n  $ref: agent.yaml", reject: true},
		{name: "service passthrough overrides nested remote", service: "docker: {imagePassthrough: true}\n" +
			"config:\n  docker: {remoteBuild: true}"},
		{name: "service remote overrides nested passthrough", service: "docker: {remoteBuild: true}\n" +
			"config:\n  docker: {imagePassthrough: true}", reject: true},
		{name: "core local default", service: "kind: hosted"},
		{name: "image defaults to remote source", service: "image: registry/image:v1\ndocker: {remoteBuild: true}",
			reject: true},
		{name: "prebuilt", service: "image: registry/image:v1\ndocker: {remoteBuild: true, imagePassthrough: true}"},
		{name: "registry connection", service: "image: registry/image:v1\nregistryConnectionId: private\n" +
			"docker: {remoteBuild: true}"},
		{name: "passthrough", service: "docker: {remoteBuild: true, imagePassthrough: true}"},
		{name: "code", service: "codeConfiguration: {}\ndocker: {remoteBuild: true}"},
		{name: "prompt", service: "kind: prompt\ndocker: {remoteBuild: true}"},
		{name: "disabled", service: "condition: false\ndocker: {remoteBuild: true}"},
		{name: "project only"},
		{name: "new registry", service: "docker: {remoteBuild: true}", noSelector: true},
		{name: "legacy connection", service: "docker: {remoteBuild: true}",
			endpoint: "https://account.services.ai.azure.com/api/projects/project"},
		{name: "other provider", service: "docker: {remoteBuild: true}", provider: "bicep"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			provider := tt.provider
			if provider == "" {
				provider = FoundryProviderName
			}
			var raw strings.Builder
			raw.WriteString("infra:\n  provider: " + provider + "\nservices:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n")
			if tt.endpoint != "" {
				raw.WriteString("    endpoint: " + tt.endpoint + "\n")
			}
			if tt.service != "" {
				raw.WriteString("  agent:\n    host: azure.ai.agent\n")
				for line := range strings.SplitSeq(tt.service, "\n") {
					raw.WriteString("    " + line + "\n")
				}
			}
			path := filepath.Join(root, "azure.yaml")
			require.NoError(t, os.WriteFile(path, []byte(raw.String()), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"),
				[]byte("kind: hosted\ndocker: {remoteBuild: true}\n"), 0o600))
			env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testACRID,
				"FOUNDRY_PROJECT_ENDPOINT":             "",
			}}
			if tt.name == "expanded existing endpoint" {
				env.get["FOUNDRY_PROJECT_ENDPOINT"] = "https://account.services.ai.azure.com/api/projects/project"
			}
			if tt.noSelector {
				delete(env.get, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
			}
			bound := acrTestBinding()
			if tt.managed {
				bound.Mode = "create"
				bound.RegistryResourceID = strings.Replace(testACRID, "/shared/", "/foundry/", 1)
				env.get["AZURE_SUBSCRIPTION_ID"] = "sub"
				env.get["AZURE_RESOURCE_GROUP"] = "foundry"
				env.get["AZURE_AI_PROJECT_ID"] = testACRProject
				env.get["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = bound.RegistryResourceID
				env.get["AZURE_CONTAINER_REGISTRY_ENDPOINT"] = "registry.azurecr.io"
			}
			if tt.canonical {
				binding, err := json.Marshal(bound)
				require.NoError(t, err)
				env.config = map[string][]byte{synthesis.ACRBindingConfigKey: binding}
				delete(env.get, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
			}
			beforeValues, beforeConfig := maps.Clone(env.get), maps.Clone(env.config)
			project := &azdext.ProjectConfig{Path: root, Infra: &azdext.InfraOptions{Provider: provider}}
			client := newValidateTestClient(t, &validateStubProjectServer{project: project}, env)
			check := NewACRRemoteBuildCheck(client)
			reads := 0
			check.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				reads++
				require.Equal(t, http.MethodGet, method)
				require.Equal(t, testACRID, path)
				mode := "AbacRepositoryPermissions"
				if tt.classic {
					mode = "LegacyRegistryPermissions"
				}
				return acrTestReply(out, map[string]any{"properties": map[string]any{"roleAssignmentMode": mode}})
			}
			result, err := check.Validate(t.Context(), provisionContext("", "", ""), &azdext.ValidationCheckRequest{})
			require.NoError(t, err)
			if tt.reject {
				require.Len(t, result.Results, 1)
				assert.Equal(t, azdext.ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_ERROR, result.Results[0].Severity)
				assert.Contains(t, result.Results[0].Message, "does not authenticate")
				assert.Contains(t, result.Results[0].Suggestion, "docker.remoteBuild: false")
				assert.Equal(t, 1, reads)
			} else {
				assert.Empty(t, result.Results)
			}
			if tt.managed {
				assert.Zero(t, reads)
			}
			assert.Equal(t, beforeValues, env.get)
			assert.Equal(t, beforeConfig, env.config)
			assert.Empty(t, env.set)
			//nolint:gosec // Test-owned path.
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, raw.String(), string(data))
			assert.NoDirExists(t, filepath.Join(root, ".azure"))
		})
	}
}

func TestACRRemoteBuildPreflightUsesOnDiskSelection(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, include := range []bool{false, true} {
		for _, layer := range []bool{false, true} {
			for _, params := range []bool{false, true} {
				for _, savedSubscription := range []bool{false, true} {
					for _, classic := range []bool{false, true} {
						t.Run(fmt.Sprintf("include=%t/layer=%t/params=%t/subscription=%t/classic=%t",
							include, layer, params, savedSubscription, classic), func(t *testing.T) {
							root := t.TempDir()
							raw := "infra:\n  provider: microsoft.foundry\n"
							path, module := "infra", "main"
							if layer {
								path, module = filepath.Join("infra", "foundry"), "project"
								raw = "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n" +
									"      path: infra/foundry\n      module: project\n"
							}
							raw += "services:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n" +
								"  agent:\n    host: azure.ai.agent\n    docker: {remoteBuild: true}\n"
							require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(raw), 0o600))
							require.NoError(t, os.MkdirAll(filepath.Join(root, path), 0o750))
							suffix := ".bicep"
							if params {
								suffix = ".bicepparam"
							}
							require.NoError(t, os.WriteFile(filepath.Join(root, path, module+suffix), []byte("// test"), 0o600))
							template := acrTestSource().armTemplate
							declared := template["parameters"].(map[string]any)
							declared["includeAcr"] = map[string]any{"type": "bool", "defaultValue": true}
							declared["existingAcrResourceId"] = map[string]any{"type": "string", "defaultValue": testACRID}
							body, err := json.Marshal(template)
							require.NoError(t, err)
							paramFile := minimalARMParametersFile(t, map[string]any{
								"includeAcr": include, "existingAcrResourceId": testACRID,
							})
							require.NoError(t, os.WriteFile(filepath.Join(root, path, module+".parameters.json"), []byte(paramFile), 0o600))
							envelope, err := json.Marshal(map[string]string{"templateJson": string(body), "parametersJson": paramFile})
							require.NoError(t, err)
							compiler := &stubCompiler{
								buildResult:      bicep.BuildResult{Compiled: string(body)},
								buildParamResult: bicep.BuildResult{Compiled: string(envelope)},
							}
							env := &resolveEnvStubEnvServer{envName: "test"}
							client := newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
							check := NewACRRemoteBuildCheck(client)
							check.compiler = compiler
							check.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
								require.Equal(t, http.MethodGet, method)
								require.Equal(t, testACRID, path)
								mode := "AbacRepositoryPermissions"
								if classic {
									mode = "LegacyRegistryPermissions"
								}
								return acrTestReply(out, map[string]any{"properties": map[string]any{"roleAssignmentMode": mode}})
							}
							values := map[string]string{"AZURE_ENV_NAME": "test"}
							if savedSubscription {
								values["AZURE_SUBSCRIPTION_ID"] = "sub"
							}
							err = check.ValidateProject(t.Context(), &azdext.ProjectConfig{Path: root}, "test", values)
							if classic {
								require.NoError(t, err)
							} else {
								require.ErrorContains(t, err, "does not authenticate")
							}
							assert.Empty(t, env.set)
							assert.Empty(t, env.config)
						})
					}
				}
			}
		}
	}
}

func TestACROnDiskNetworkingUsesEffectiveInputs(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, route := range []struct {
		name, path, module string
		layer              bool
	}{
		{name: "root", path: "infra", module: "main"},
		{name: "custom root", path: "iac/custom", module: "selected"},
		{name: "layer", path: "infra/foundry", module: "main", layer: true},
		{name: "custom layer", path: "iac/foundry", module: "project", layer: true},
	} {
		for _, selection := range []string{"JSON", "Bicepparam", "template default"} {
			for _, tt := range []struct {
				name, registryID, wantError string
				noACR, public, abac, local  bool
			}{
				{name: "external registry", registryID: testACRID},
				{name: "auto-created registry", wantError: "auto-created Azure Container Registry"},
				{name: "disabled registry", noACR: true},
				{name: "disabled network", public: true},
				{name: "remote ABAC", registryID: testACRID, abac: true, wantError: "does not authenticate"},
				{name: "local ABAC", registryID: testACRID, abac: true, local: true},
			} {
				for _, action := range []string{"deploy", "preview", "preflight"} {
					t.Run(route.name+"/"+selection+"/"+tt.name+"/"+action, func(t *testing.T) {
						root := t.TempDir()
						raw := "infra:\n  provider: microsoft.foundry\n" +
							"  path: " + route.path + "\n  module: " + route.module + "\n"
						options := &azdext.ProvisioningOptions{
							Provider: FoundryProviderName, Path: filepath.FromSlash(route.path), Module: route.module,
						}
						if route.layer {
							raw = "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n" +
								"      path: " + route.path + "\n      module: " + route.module + "\n"
							options.Name = "foundry"
						}
						raw += "services:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n" +
							"    network:\n      peSubnet:\n" +
							"        vnet: /subscriptions/sub/resourceGroups/network/" +
							"providers/Microsoft.Network/virtualNetworks/vnet\n        name: pe-subnet\n" +
							"  agent:\n    host: azure.ai.agent\n    kind: hosted\n" +
							"    docker: {remoteBuild: " + strconv.FormatBool(!tt.local) + "}\n"
						infra := filepath.Join(root, filepath.FromSlash(route.path))
						require.NoError(t, os.MkdirAll(infra, 0o750))
						template := acrTestSource().armTemplate
						declared, ok := template["parameters"].(map[string]any)
						require.True(t, ok)
						declared["includeAcr"] = map[string]any{"type": "bool", "defaultValue": true}
						declared["existingAcrResourceId"] = map[string]any{"type": "string", "defaultValue": ""}
						declared["enableNetworkIsolation"] = map[string]any{"type": "bool", "defaultValue": false}
						values := map[string]any{"includeAcr": !tt.noACR, "existingAcrResourceId": tt.registryID}
						if selection == "template default" {
							declared["existingAcrResourceId"] = map[string]any{
								"type": "string", "defaultValue": tt.registryID,
							}
							delete(values, "existingAcrResourceId")
						}
						if tt.public {
							values["enableNetworkIsolation"] = false
						}
						body, err := json.Marshal(template)
						require.NoError(t, err)
						paramFile := minimalARMParametersFile(t, values)
						bicepFile := filepath.Join(infra, route.module+".bicep")
						files := map[string]string{
							filepath.Join(root, "azure.yaml"): raw,
							bicepFile:                         "targetScope = 'subscription'\n",
							filepath.Join(infra, route.module+".parameters.json"): paramFile,
						}
						compiler := &stubCompiler{buildResult: bicep.BuildResult{Compiled: string(body)}}
						paramPath := filepath.Join(infra, route.module+".bicepparam")
						if selection == "Bicepparam" {
							files[paramPath] = "using './" + route.module + ".bicep'\n"
							envelope, err := json.Marshal(map[string]string{
								"templateJson": string(body), "parametersJson": paramFile,
							})
							require.NoError(t, err)
							compiler.buildParamResult = bicep.BuildResult{Compiled: string(envelope)}
						}
						for path, content := range files {
							require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
						}
						env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
							envKeySubscriptionID: "sub", envKeyLocation: "eastus2", envKeyResourceGroup: "foundry",
						}, config: map[string][]byte{"unrelated": []byte(`{"keep":true}`)}}
						beforeValues, beforeConfig := maps.Clone(env.get), maps.Clone(env.config)
						client := newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
						reads := 0
						readRegistry := func(_ context.Context, method, path, _ string, out any) error {
							reads++
							require.Equal(t, http.MethodGet, method)
							if path != testACRID {
								return &azcore.ResponseError{StatusCode: http.StatusNotFound}
							}
							mode := "LegacyRegistryPermissions"
							if tt.abac {
								mode = "AbacRepositoryPermissions"
							}
							return acrTestReply(out, map[string]any{"properties": map[string]any{
								"roleAssignmentMode": mode,
							}})
						}
						submitted := false
						if action == "preflight" {
							check := NewACRRemoteBuildCheck(client)
							check.compiler, check.acrRequest = compiler, readRegistry
							err = check.ValidateProject(t.Context(), &azdext.ProjectConfig{Path: root}, "test", env.get)
						} else {
							p := &FoundryProvisioningProvider{azdClient: client, bicepCliInstance: compiler}
							require.NoError(t, p.Initialize(t.Context(), root, options))
							assert.Empty(t, env.set)
							assert.Equal(t, beforeConfig, env.config)
							p.credential, p.acrRequest = acrTestCredential{}, readRegistry
							p.resourceGroupState = func(context.Context) (map[string]*string, bool, error) {
								return nil, true, nil
							}
							p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
								Retry: policy.RetryOptions{MaxRetries: -1},
								Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
									require.Empty(t, tt.wantError)
									method := http.MethodPut
									if action == "preview" {
										method = http.MethodPost
										assert.True(t, strings.HasSuffix(request.URL.Path, "/whatIf"))
									}
									assert.Equal(t, method, request.Method)
									var payload struct {
										Properties struct {
											Parameters map[string]any `json:"parameters"`
										} `json:"properties"`
									}
									require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
									source := &templateSource{
										armTemplate: template, parameters: payload.Properties.Parameters,
									}
									assert.Equal(t, tt.registryID, parameterValue(source, "existingAcrResourceId"))
									assert.Equal(t, !tt.noACR, parameterValue(source, "includeAcr"))
									assert.Equal(t, !tt.public, parameterValue(source, "enableNetworkIsolation"))
									submitted = true
									return nil, errors.New("stop at ARM submission")
								}),
							}}
							if action == "preview" {
								_, err = p.Preview(t.Context(), func(string) {})
							} else {
								_, err = p.Deploy(t.Context(), func(string) {})
							}
						}
						switch {
						case tt.wantError != "":
							require.ErrorContains(t, err, tt.wantError)
							assert.False(t, submitted)
							if !tt.abac {
								assert.Zero(t, reads)
							}
						case action == "preflight":
							require.NoError(t, err)
						default:
							require.ErrorContains(t, err, "stop at ARM submission")
							assert.True(t, submitted)
						}
						if action != "deploy" || tt.wantError != "" {
							assert.Empty(t, env.set)
							assert.Equal(t, beforeConfig, env.config)
						}
						assert.Equal(t, beforeValues, env.get)
						if action != "preflight" || !tt.local {
							if selection == "Bicepparam" {
								assert.Equal(t, []string{paramPath}, compiler.buildParamCalls)
								assert.Empty(t, compiler.buildCalls)
							} else {
								assert.Equal(t, []string{bicepFile}, compiler.buildCalls)
								assert.Empty(t, compiler.buildParamCalls)
							}
						}
						for path, content := range files {
							//nolint:gosec // Test-owned path.
							actual, readErr := os.ReadFile(path)
							require.NoError(t, readErr)
							assert.Equal(t, content, string(actual))
						}
						assert.NoDirExists(t, filepath.Join(root, ".azure"))
					})
				}
			}
		}
	}
}

func TestACRBuildFailurePreservesOnlyActualSuggestions(t *testing.T) {
	for _, tt := range []struct {
		name, want string
		err        error
	}{
		{name: "project read", err: errors.New("read project: unavailable")},
		{name: "binding", err: errors.New("invalid deployed ACR binding")},
		{name: "network", err: errors.New("registry request: connection refused")},
		{name: "forbidden", err: &azcore.ResponseError{StatusCode: http.StatusForbidden}},
		{name: "ABAC", err: exterrors.Validation(exterrors.CodeInvalidServiceConfig,
			"unsupported registry build", acrRemoteBuildSuggestion), want: acrRemoteBuildSuggestion},
		{name: "service", err: &azdext.ServiceError{
			Message: "service unavailable", Suggestion: "retry when the service is available",
		}, want: "retry when the service is available"},
		{name: "wrapped", err: fmt.Errorf("validate: %w", exterrors.Validation(
			exterrors.CodeInvalidParameter, "invalid input", "correct the input")), want: "correct the input"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := acrBuildCheckFailure(tt.err)
			require.Len(t, result.Results, 1)
			assert.Equal(t, tt.err.Error(), result.Results[0].Message)
			assert.Equal(t, tt.want, result.Results[0].Suggestion)
		})
	}
}
func TestACRRemoteBuildRejectedBeforeLifecycleWrites(t *testing.T) {
	p, env := acrTestProvider(t)
	p.acrRemoteBuild, p.acrBindingRecovered = true, true
	p.acrBinding = acrTestBinding()
	p.acrPending = acrTestPending()
	before := *p.acrPending
	p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
		require.Equal(t, http.MethodGet, method)
		if path == testACRID {
			return acrTestReply(out, map[string]any{"properties": map[string]any{
				"roleAssignmentMode": "AbacRepositoryPermissions",
			}})
		}
		require.Equal(t, p.acrPending.assignmentID(), path)
		return &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}
	require.ErrorContains(t, p.prepareACRAccess(t.Context(), acrTestSource()), "does not authenticate")
	assert.True(t, p.acrBindingRecovered)
	assert.Equal(t, before, *p.acrPending)
	assert.Empty(t, env.config)
	assert.Empty(t, env.set)
}

func TestACRRemoteBuildLookupFailureStopsPreflight(t *testing.T) {
	p, _ := acrTestProvider(t)
	p.acrRemoteBuild = true
	p.acrRequest = func(context.Context, string, string, string, any) error {
		return errors.New("registry unavailable")
	}
	require.ErrorContains(t, p.validateACRRemoteBuild(t.Context(), acrTestSource()), "registry unavailable")
}

func TestACRRemoteBuildRequiresPreview(t *testing.T) {
	for _, include := range []bool{false, true} {
		t.Run(strconv.FormatBool(include), func(t *testing.T) {
			p, env := acrTestProvider(t)
			t.Setenv(synthesis.ACRReuseEnvVar, "false")
			p.acrRemoteBuild = true
			p.acrRequest = func(context.Context, string, string, string, any) error {
				t.Fatal("disabled reuse must fail before registry requests")
				return nil
			}
			src := acrTestSource()
			src.parameters["includeAcr"] = map[string]any{"value": include}
			err := p.validateACRRemoteBuild(t.Context(), src)
			require.ErrorContains(t, err, synthesis.ACRReuseEnvVar+"=true")
			assert.Empty(t, env.config)
			assert.Empty(t, env.set)
		})
	}
}

func TestACRRemoteBuildPreservesManagedBinding(t *testing.T) {
	for _, include := range []bool{false, true} {
		t.Run(strconv.FormatBool(include), func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.acrRemoteBuild = true
			p.acrBinding = acrTestBinding()
			p.acrBinding.Mode = "create"
			p.acrBinding.RegistryResourceID = strings.Replace(testACRID, "/shared/", "/foundry/", 1)
			p.acrRequest = func(context.Context, string, string, string, any) error {
				t.Fatal("managed registries must not use external registry validation")
				return nil
			}
			src := acrTestSource()
			src.parameters["includeAcr"] = map[string]any{"value": include}
			delete(src.parameters, "existingAcrResourceId")
			require.NoError(t, p.validateACRRemoteBuild(t.Context(), src))
			require.NoError(t, p.prepareACRAccess(t.Context(), src))
			assert.Empty(t, env.config)
			assert.Empty(t, env.set)
		})
	}
}

func TestACRRemoteBuildWithInfrastructureDisabled(t *testing.T) {
	for _, selector := range []string{"", testACRID} {
		for _, action := range []string{"validate", "prepare"} {
			t.Run(action+"/selector="+strconv.FormatBool(selector != ""), func(t *testing.T) {
				p, env := acrTestProvider(t)
				p.acrRemoteBuild, p.acrBindingRecovered = true, true
				p.acrBinding = acrTestBinding()
				p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
					require.Equal(t, http.MethodGet, method)
					require.Equal(t, testACRID, path)
					return acrTestReply(out, map[string]any{"properties": map[string]any{
						"roleAssignmentMode": "AbacRepositoryPermissions",
					}})
				}
				src := acrTestSource()
				src.parameters["includeAcr"] = map[string]any{"value": false}
				src.parameters["existingAcrResourceId"] = map[string]any{"value": selector}
				var err error
				if action == "validate" {
					err = p.validateACRRemoteBuild(t.Context(), src)
				} else {
					err = p.prepareACRAccess(t.Context(), src)
				}
				require.ErrorContains(t, err, "does not authenticate")
				assert.True(t, p.acrBindingRecovered)
				assert.Empty(t, env.config)
				assert.Empty(t, env.set)
			})
		}
	}
}

func TestACRRemoteBuildRejectsBeforePersistingPromptChoices(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, condition := range []string{"", "${AZURE_LOCATION/eastus2/true}", "${AZURE_SUBSCRIPTION_ID/sub/true}"} {
		for _, test := range []struct{ abac, onDisk bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
			t.Run(fmt.Sprintf("condition=%s/ABAC=%t/onDisk=%t", condition, test.abac, test.onDisk), func(t *testing.T) {
				root := t.TempDir()
				raw := "infra:\n  provider: microsoft.foundry\nservices:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n" +
					"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    docker: {remoteBuild: true}\n"
				if condition != "" {
					raw += "    condition: " + condition + "\n"
				}
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(raw), 0o600))
				env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
					"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testACRID,
				}}
				if test.onDisk {
					delete(env.get, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
					infra := filepath.Join(root, "infra")
					require.NoError(t, os.Mkdir(infra, 0o750))
					template := "targetScope = 'subscription'\nparam includeAcr bool = true\n" +
						"param existingAcrResourceId string = '" + testACRID + "'\n" +
						"param acrPullAssigned bool = false\nparam acrAccessOperationId string = ''\n" +
						"param acrPullRoleDefinitionId string = ''\nparam acrOwnedAssignments array = []\n"
					require.NoError(t, os.WriteFile(filepath.Join(infra, "main.bicep"), []byte(template), 0o600))
				}
				prompt := &resolveEnvStubPromptServer{subscriptionID: "sub", location: "eastus2"}
				p := &FoundryProvisioningProvider{azdClient: newResolveEnvTestClient(t, env, prompt)}
				require.NoError(t, p.Initialize(t.Context(), root,
					&azdext.ProvisioningOptions{Provider: FoundryProviderName}))
				assert.Equal(t, 1, prompt.subscriptionN)
				assert.Equal(t, 1, prompt.locationN)
				assert.True(t, p.acrRemoteBuild, "final prompted values must determine remote-build demand")
				assert.Empty(t, env.set)
				p.credential = acrTestCredential{}
				p.resourceGroupState = func(context.Context) (map[string]*string, bool, error) {
					return nil, true, nil
				}
				attempted := false
				p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
					Retry: policy.RetryOptions{MaxRetries: -1},
					Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
						require.Equal(t, http.MethodPut, request.Method)
						assert.Equal(t, "sub", env.set[envKeySubscriptionID])
						assert.Equal(t, "eastus2", env.set[envKeyLocation])
						attempted = true
						return nil, errors.New("stop at deployment submission")
					}),
				}}
				p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
					require.Equal(t, http.MethodGet, method)
					if path == testACRID {
						mode := "LegacyRegistryPermissions"
						if test.abac {
							mode = "AbacRepositoryPermissions"
						}
						return acrTestReply(out, map[string]any{"properties": map[string]any{"roleAssignmentMode": mode}})
					}
					return &azcore.ResponseError{StatusCode: http.StatusNotFound}
				}
				_, err := p.Deploy(t.Context(), func(string) {})
				require.Error(t, err)
				if test.abac {
					require.ErrorContains(t, err, "does not authenticate")
					assert.False(t, attempted)
					assert.Empty(t, env.set)
					assert.Empty(t, env.config)
					assert.Nil(t, p.acrPending)
				} else {
					require.ErrorContains(t, err, "stop at deployment submission")
					assert.True(t, attempted)
					assert.Equal(t, "sub", env.set[envKeySubscriptionID])
					assert.Equal(t, "eastus2", env.set[envKeyLocation])
				}
			})
		}
	}
}

func TestACROnDiskLocalBuildValidatesPromptedScopeBeforeWrites(t *testing.T) {
	for _, route := range []struct {
		name, path, module string
		layer              bool
	}{
		{name: "root", path: "infra", module: "main"},
		{name: "custom root", path: "iac/custom", module: "selected"},
		{name: "layer", path: "infra/foundry", module: "main", layer: true},
		{name: "custom layer", path: "iac/foundry", module: "project", layer: true},
	} {
		for _, params := range []string{"JSON", "Bicepparam"} {
			for _, tt := range []struct {
				name, subscription, wantError string
				sameGroup, noPrompt           bool
				noLocation, compileFailure    bool
			}{
				{name: "matching scope", subscription: "sub"},
				{name: "subscription mismatch", subscription: "other", wantError: "same subscription"},
				{name: "deletion scope", subscription: "sub", sameGroup: true, wantError: "resource group"},
				{name: "no prompt", noPrompt: true, wantError: "AZURE_SUBSCRIPTION_ID is required"},
				{name: "no location prompt", subscription: "sub", noLocation: true, wantError: "AZURE_LOCATION is required"},
				{name: "compile failure", subscription: "sub", compileFailure: true, wantError: "invalid authored template"},
			} {
				t.Run(route.name+"/"+params+"/"+tt.name, func(t *testing.T) {
					t.Setenv(synthesis.ACRReuseEnvVar, "true")
					root := t.TempDir()
					options := &azdext.ProvisioningOptions{Provider: FoundryProviderName}
					raw := "infra:\n  provider: microsoft.foundry\n"
					if route.layer {
						raw = "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n" +
							"      path: " + route.path + "\n      module: " + route.module + "\n"
						options.Name = "foundry"
					} else if route.name != "root" {
						raw += "  path: " + route.path + "\n  module: " + route.module + "\n"
					}
					if route.name != "root" {
						options.Path, options.Module = filepath.FromSlash(route.path), route.module
					}
					raw += "services:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n" +
						"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    docker: {remoteBuild: false}\n"
					infra := filepath.Join(root, filepath.FromSlash(route.path))
					require.NoError(t, os.MkdirAll(infra, 0o750))
					bicepFile := filepath.Join(infra, route.module+".bicep")
					files := map[string]string{
						filepath.Join(root, "azure.yaml"): raw,
						bicepFile: "targetScope = 'subscription'\nparam includeAcr bool = true\n" +
							"param existingAcrResourceId string = ''\nparam resourceGroupName string\n" +
							"param acrPullAssigned bool = false\nparam acrAccessOperationId string = ''\n" +
							"param acrPullRoleDefinitionId string = ''\nparam acrOwnedAssignments array = []\n",
					}
					values := map[string]any{"includeAcr": true, "existingAcrResourceId": testACRID}
					group := defaultResourceGroupName("test")
					if route.layer {
						group += "-foundry"
					}
					if tt.sameGroup {
						group = "shared"
						values["resourceGroupName"] = group
					}
					paramFile := minimalARMParametersFile(t, values)
					files[filepath.Join(infra, route.module+".parameters.json")] = paramFile
					template := acrTestSource().armTemplate
					declared, ok := template["parameters"].(map[string]any)
					require.True(t, ok)
					declared["includeAcr"] = map[string]any{"type": "bool", "defaultValue": true}
					declared["existingAcrResourceId"] = map[string]any{"type": "string", "defaultValue": ""}
					declared["resourceGroupName"] = map[string]any{"type": "string"}
					body, err := json.Marshal(template)
					require.NoError(t, err)
					compiler := &stubCompiler{buildResult: bicep.BuildResult{Compiled: string(body)}}
					paramPath := filepath.Join(infra, route.module+".bicepparam")
					if params == "Bicepparam" {
						files[paramPath] = "using './" + route.module + ".bicep'\n" +
							"param existingAcrResourceId = '" + testACRID + "'\n" +
							"param resourceGroupName = '" + group + "'\n"
						envelope, err := json.Marshal(map[string]string{
							"templateJson": string(body), "parametersJson": paramFile,
						})
						require.NoError(t, err)
						compiler.buildParamResult = bicep.BuildResult{Compiled: string(envelope)}
						files[filepath.Join(infra, route.module+".parameters.json")] =
							minimalARMParametersFile(t, map[string]any{"existingAcrResourceId": "ignored"})
					}
					if tt.compileFailure {
						compiler.buildErr = errors.New("invalid authored template")
						compiler.buildParamErr = compiler.buildErr
					}
					for path, content := range files {
						require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
					}
					env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{},
						config: map[string][]byte{"unrelated": []byte(`{"keep":true}`)}}
					beforeConfig := maps.Clone(env.config)
					prompt := &resolveEnvStubPromptServer{subscriptionID: tt.subscription, location: "eastus2"}
					if tt.noPrompt {
						prompt.subscriptionErr = status.Error(codes.FailedPrecondition, "prompt required: no terminal")
					}
					if tt.noLocation {
						prompt.locationErr = status.Error(codes.FailedPrecondition, "prompt required: no terminal")
					}
					p := &FoundryProvisioningProvider{
						azdClient: newResolveEnvTestClient(t, env, prompt), bicepCliInstance: compiler,
					}
					attempted := false
					err = p.Initialize(t.Context(), root, options)
					if !tt.noPrompt && !tt.noLocation {
						require.NoError(t, err)
						assert.False(t, p.acrRemoteBuild)
						assert.Empty(t, p.greenfieldAcrResourceID)
						assert.Empty(t, env.set, "selected on-disk inputs must be validated before saving prompts")
						assert.Equal(t, 1, prompt.subscriptionN)
						assert.Equal(t, 1, prompt.locationN)
						p.credential = acrTestCredential{}
						p.resourceGroupState = func(context.Context) (map[string]*string, bool, error) {
							return nil, true, nil
						}
						p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
							require.Empty(t, tt.wantError, "invalid final scope must fail before lifecycle requests")
							require.Equal(t, http.MethodGet, method)
							if path == testACRID {
								return acrTestReply(out, map[string]any{"properties": map[string]any{
									"roleAssignmentMode": "AbacRepositoryPermissions",
								}})
							}
							return &azcore.ResponseError{StatusCode: http.StatusNotFound}
						}
						p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
							Retry: policy.RetryOptions{MaxRetries: -1},
							Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
								require.Empty(t, tt.wantError, "invalid final scope must not submit infrastructure")
								require.Equal(t, http.MethodPut, request.Method)
								assert.Equal(t, "sub", env.set[envKeySubscriptionID])
								assert.Equal(t, "eastus2", env.set[envKeyLocation])
								assert.NotEmpty(t, env.config[acrPendingConfigKey])
								attempted = true
								return nil, errors.New("stop at deployment submission")
							}),
						}}
						_, err = p.Deploy(t.Context(), func(string) {})
						if params == "Bicepparam" {
							assert.Equal(t, []string{paramPath}, compiler.buildParamCalls)
							assert.Empty(t, compiler.buildCalls)
							require.Len(t, compiler.buildParamEnvs, 1)
							assert.Contains(t, compiler.buildParamEnvs[0], envKeySubscriptionID+"="+tt.subscription)
							assert.Contains(t, compiler.buildParamEnvs[0], envKeyLocation+"=eastus2")
						} else {
							assert.Equal(t, []string{bicepFile}, compiler.buildCalls)
							assert.Empty(t, compiler.buildParamCalls)
						}
					}
					if tt.wantError != "" {
						require.ErrorContains(t, err, tt.wantError)
						assert.False(t, attempted)
						assert.Empty(t, env.set)
						assert.Equal(t, beforeConfig, env.config)
						assert.Nil(t, p.acrPending)
						assert.Nil(t, p.acrBinding)
					} else {
						require.ErrorContains(t, err, "stop at deployment submission")
						assert.True(t, attempted)
						assert.Empty(t, p.acrPromptedEnv)
						assert.False(t, p.acrDeferPromptedEnv)
					}
					assert.Empty(t, env.get)
					for path, content := range files {
						//nolint:gosec // Test-owned path.
						actual, readErr := os.ReadFile(path)
						require.NoError(t, readErr)
						assert.Equal(t, content, string(actual))
					}
					assert.NoDirExists(t, filepath.Join(root, ".azure"))
				})
			}
		}
	}
}
