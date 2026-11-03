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
	"strings"
	"testing"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

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
	for _, tt := range []struct {
		name, service, endpoint, provider string
		classic, canonical, noSelector    bool
		reject                            bool
	}{
		{name: "remote ABAC", service: "docker: {remoteBuild: true}", reject: true},
		{name: "generated remote default", service: "kind: hosted\ndocker: {remoteBuild: true}", reject: true},
		{name: "canonical only", service: "docker: {remoteBuild: true}", canonical: true, reject: true},
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
		{name: "prebuilt", service: "image: registry/image:v1\ndocker: {remoteBuild: true}"},
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
			raw.WriteString("infra:\n  provider: " + provider + "\nservices:\n  project:\n    host: azure.ai.project\n")
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
			}}
			if tt.noSelector {
				delete(env.get, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
			}
			if tt.canonical {
				binding, err := json.Marshal(acrTestBinding())
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
	for _, layer := range []bool{false, true} {
		for _, params := range []bool{false, true} {
			for _, savedSubscription := range []bool{false, true} {
				for _, classic := range []bool{false, true} {
					t.Run(fmt.Sprintf("layer=%t/params=%t/subscription=%t/classic=%t",
						layer, params, savedSubscription, classic), func(t *testing.T) {
						root := t.TempDir()
						raw := "infra:\n  provider: microsoft.foundry\n"
						path, module := "infra", "main"
						if layer {
							path, module = filepath.Join("infra", "foundry"), "project"
							raw = "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n" +
								"      path: infra/foundry\n      module: project\n"
						}
						raw += "services:\n  project:\n    host: azure.ai.project\n" +
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
							"includeAcr": true, "existingAcrResourceId": testACRID,
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

func TestACRRemoteBuildRejectsBeforePersistingPromptChoices(t *testing.T) {
	for _, test := range []struct{ abac, onDisk bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		t.Run(fmt.Sprintf("ABAC=%t/onDisk=%t", test.abac, test.onDisk), func(t *testing.T) {
			root := t.TempDir()
			raw := "infra:\n  provider: microsoft.foundry\nservices:\n  project:\n    host: azure.ai.project\n" +
				"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    docker: {remoteBuild: true}\n"
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
					raw += "services:\n  project:\n    host: azure.ai.project\n" +
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
