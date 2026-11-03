// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS

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

	"azure.ai.projects/internal/synthesis"
	"azure.ai.projects/internal/testutil"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestACRRemoteBuildFeatureGate(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	for _, state := range []string{
		"selector", "canonical", "pending", "json", "bicepparam", "create", "legacy",
		"remote selector", "remote canonical", "remote pending", "remote json", "remote bicepparam",
	} {
		for _, layer := range []bool{false, true} {
			t.Run(state+"/"+map[bool]string{false: "root", true: "layer"}[layer], func(t *testing.T) {
				root := t.TempDir()
				intent := strings.TrimPrefix(state, "remote ")
				format, id := "json", ""
				if intent == "json" || intent == "bicepparam" {
					format, id = intent, testACRID
				}
				testutil.ACRParameters(t, root, layer, format, id)
				if strings.HasPrefix(state, "remote ") {
					path := filepath.Join(root, "azure.yaml")
					//nolint:gosec // isolated project fixture
					raw, err := os.ReadFile(path)
					require.NoError(t, err)
					raw = []byte(strings.ReplaceAll(string(raw), "kind: hosted\n",
						"kind: hosted\n    docker: {remoteBuild: true}\n"))
					//nolint:gosec // isolated project fixture
					require.NoError(t, os.WriteFile(path, raw, 0o600))
				}
				env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
					"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_RESOURCE_GROUP": "foundry",
				}, config: map[string][]byte{}}
				switch intent {
				case "selector", "legacy":
					env.get["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testACRID
					if intent == "legacy" {
						env.get["USE_EXISTING_AI_PROJECT"] = "true"
					}
				case "canonical":
					value, err := json.Marshal(acrTestBinding())
					require.NoError(t, err)
					env.config[synthesis.ACRBindingConfigKey] = value
				case "pending":
					value, err := json.Marshal(acrTestPending())
					require.NoError(t, err)
					env.config[acrPendingConfigKey] = value
				}
				project := &azdext.ProjectConfig{Path: root}
				client := newValidateTestClient(t, &validateStubProjectServer{project: project}, env)
				check := NewACRRemoteBuildCheck(client)
				azureCalls := 0
				check.acrRequest = func(context.Context, string, string, string, any) error {
					azureCalls++
					return errors.New("unexpected Azure request")
				}
				beforeProject := proto.Clone(project)
				beforeValues, beforeConfig := maps.Clone(env.get), maps.Clone(env.config)
				beforeFiles := testutil.Snapshot(t, root)
				configRoot := os.Getenv("AZD_CONFIG_DIR")
				beforeUserConfig := testutil.Snapshot(t, configRoot)
				result, err := check.Validate(t.Context(), provisionContext("", "", ""), &azdext.ValidationCheckRequest{})
				require.NoError(t, err)
				hardErr := check.ValidateProject(t.Context(), project, "test", env.get)
				if state == "create" || state == "legacy" {
					require.Empty(t, result.Results)
					require.NoError(t, hardErr)
				} else {
					require.Len(t, result.Results, 1)
					require.Equal(t, ACRRemoteBuildRuleID, result.Results[0].DiagnosticId)
					require.Equal(t, azdext.ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_ERROR,
						result.Results[0].Severity)
					require.Contains(t, result.Results[0].Message, synthesis.ACRReuseEnvVar+"=true")
					require.Contains(t, result.Results[0].Suggestion, synthesis.ACRReuseEnvVar+"=true")
					requireReuseDisabled(t, hardErr)
				}
				require.Zero(t, azureCalls)
				require.Empty(t, env.set)
				require.Equal(t, beforeValues, env.get)
				require.Equal(t, beforeConfig, env.config)
				require.True(t, proto.Equal(beforeProject, project))
				require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
				require.Equal(t, beforeUserConfig, testutil.Snapshot(t, configRoot))
			})
		}
	}
}

func TestACRRemoteBuildLocalInputBeforeGate(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	root := t.TempDir()
	testutil.ACRParameters(t, root, false, "json", testACRID)
	raw := "infra:\n  provider: microsoft.foundry\nservices:\n  project:\n    host: azure.ai.project\n" +
		"  agent:\n    host: azure.ai.agent\n    docker: {remoteBuild: invalid}\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(raw), 0o600))
	project := &azdext.ProjectConfig{Path: root}
	env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testACRID,
	}}
	check := NewACRRemoteBuildCheck(newValidateTestClient(t, &validateStubProjectServer{project: project}, env))
	azureCalls := 0
	check.acrRequest = func(context.Context, string, string, string, any) error {
		azureCalls++
		return errors.New("unexpected Azure request")
	}
	before := testutil.Snapshot(t, root)
	err := check.ValidateProject(t.Context(), project, "test", env.get)
	require.ErrorContains(t, err, "cannot unmarshal")
	require.NotContains(t, err.Error(), synthesis.ACRReuseEnvVar)
	require.Zero(t, azureCalls)
	require.Empty(t, env.set)
	require.Empty(t, env.config)
	require.Equal(t, before, testutil.Snapshot(t, root))
}

func requireReuseDisabled(t *testing.T, err error) {
	t.Helper()
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, "feature_not_enabled", local.Code)
	require.Equal(t, azdext.LocalErrorCategoryCompatibility, local.Category)
	require.Contains(t, local.Message, synthesis.ACRReuseEnvVar+"=true")
}

func TestACRReuseDisabledProviderEntrypoints(t *testing.T) {
	operations := map[string]func(*FoundryProvisioningProvider) error{
		"state": func(p *FoundryProvisioningProvider) error {
			_, err := p.State(t.Context(), &azdext.ProvisioningStateOptions{})
			return err
		},
		"deploy": func(p *FoundryProvisioningProvider) error {
			_, err := p.Deploy(t.Context(), func(string) {})
			return err
		},
		"preview": func(p *FoundryProvisioningProvider) error {
			_, err := p.Preview(t.Context(), func(string) {})
			return err
		},
		"down": func(p *FoundryProvisioningProvider) error {
			_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			return err
		},
		"parameters": func(p *FoundryProvisioningProvider) error {
			_, err := p.Parameters(t.Context())
			return err
		},
		"planned outputs": func(p *FoundryProvisioningProvider) error {
			_, err := p.PlannedOutputs(t.Context())
			return err
		},
		"recovery": func(p *FoundryProvisioningProvider) error {
			return p.recoverDeployedACRBinding(t.Context())
		},
		"partial recovery": func(p *FoundryProvisioningProvider) error {
			return p.recoverPartialACRBinding(t.Context(), "", nil)
		},
		"cleanup": func(p *FoundryProvisioningProvider) error {
			return p.cleanupACRAccess(t.Context())
		},
	}
	for _, state := range []string{"selector", "binding without aliases", "pending without aliases"} {
		for name, operation := range operations {
			t.Run(state+"/"+name, func(t *testing.T) {
				p, env := acrTestProvider(t)
				switch state {
				case "selector":
					p.synthesisEnv = map[string]string{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testACRID}
				case "binding without aliases":
					p.acrBinding = acrTestBinding()
				case "pending without aliases":
					p.acrPending = acrTestPending()
				}
				env.config = map[string][]byte{"sentinel": []byte(`{"keep":true}`)}
				before := maps.Clone(env.config)
				azureCalls := 0
				p.acrRequest = func(context.Context, string, string, string, any) error {
					azureCalls++
					return errors.New("unexpected Azure request")
				}
				t.Setenv(synthesis.ACRReuseEnvVar, "")
				requireReuseDisabled(t, operation(p))
				require.Zero(t, azureCalls)
				require.Empty(t, env.set)
				require.Equal(t, before, env.config)
			})
		}
	}
}

func TestACRReuseDisabledBYOLifecycle(t *testing.T) {
	for _, intent := range []string{"parameters", "binding without aliases", "pending without aliases"} {
		for _, operation := range []string{"deploy", "preview"} {
			t.Run(intent+"/"+operation, func(t *testing.T) {
				t.Setenv(synthesis.ACRReuseEnvVar, "false")
				t.Setenv(synthesis.BYOAKSEnvVar, "true")
				t.Setenv("AZD_CONFIG_DIR", t.TempDir())
				p, fake := offlineBYOProvider(t)
				env := &resolveEnvStubEnvServer{
					get: map[string]string{"sentinel": "keep"},
					config: map[string][]byte{
						"sentinel": []byte(`{"keep":true}`),
					},
				}
				p.azdClient = newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})
				registryID := strings.Replace(testACRID, "/subscriptions/sub/", "/subscriptions/"+p.subID+"/", 1)
				switch intent {
				case "parameters":
					p.synthResult.Parameters["includeAcr"] = true
					p.synthResult.Parameters["existingAcrResourceId"] = registryID
				case "binding without aliases":
					p.acrBinding = acrTestBinding()
					p.acrBinding.RegistryResourceID = registryID
					p.acrBinding.ProjectResourceID = offlineProjectID
				case "pending without aliases":
					p.acrPending = acrTestPending()
					p.acrPending.RegistryResourceID = registryID
					p.acrPending.RoleDefinitionID = "/subscriptions/" + p.subID +
						"/providers/Microsoft.Authorization/roleDefinitions/" + acrPullRole
					p.acrPending.DeploymentIDs = acrAccessDeploymentIDs(registryID, testACRToken)
					p.acrPending.ResourceGroupID = "/subscriptions/" + p.subID + "/resourceGroups/" + p.rgName
				}
				p.acrPromptedEnv = map[string]string{
					envKeySubscriptionID: p.subID, envKeyLocation: p.location,
				}
				beforeValues, beforeConfig := maps.Clone(env.get), maps.Clone(env.config)
				beforeVirtual, beforePrompted := maps.Clone(p.virtualEnv), maps.Clone(p.acrPromptedEnv)
				beforeFiles := testutil.Snapshot(t, p.projectPath)
				configRoot := os.Getenv("AZD_CONFIG_DIR")
				beforeUserConfig := testutil.Snapshot(t, configRoot)
				azureCalls := 0
				p.acrRequest = func(context.Context, string, string, string, any) error {
					azureCalls++
					return errors.New("unexpected Azure request")
				}
				var err error
				if operation == "deploy" {
					_, err = p.Deploy(t.Context(), func(string) {})
				} else {
					_, err = p.Preview(t.Context(), func(string) {})
				}
				requireReuseDisabled(t, err)
				require.Zero(t, azureCalls)
				require.Empty(t, fake.requests)
				require.Empty(t, env.set)
				require.Equal(t, beforeValues, env.get)
				require.Equal(t, beforeConfig, env.config)
				require.Equal(t, beforeVirtual, p.virtualEnv)
				require.Equal(t, beforePrompted, p.acrPromptedEnv)
				require.Equal(t, beforeFiles, testutil.Snapshot(t, p.projectPath))
				require.Equal(t, beforeUserConfig, testutil.Snapshot(t, configRoot))
			})
		}
	}
}

func TestACRReuseDisabledBeforeLifecycleWrites(t *testing.T) {
	for _, state := range []string{"selector", "canonical", "pending", "ejected parameters", "ejected bicepparam"} {
		for _, layer := range []bool{false, true} {
			t.Run(state+"/layer="+map[bool]string{false: "false", true: "true"}[layer], func(t *testing.T) {
				p, env := acrTestProvider(t)
				t.Setenv(synthesis.ACRReuseEnvVar, "")
				root := t.TempDir()
				raw := "services:\n  project:\n    host: azure.ai.project\n" +
					"  agent:\n    host: azure.ai.agent\n    kind: hosted\n"
				options := &azdext.ProvisioningOptions{Provider: FoundryProviderName}
				if layer {
					raw += "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n" +
						"      path: infra/foundry\n"
					options.Name, options.Path = "foundry", filepath.Join("infra", "foundry")
				}
				path := filepath.Join(root, "azure.yaml")
				require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
				env.get = map[string]string{
					"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_RESOURCE_GROUP": "foundry",
					"AZURE_FOUNDRY_RESOURCE_GROUP": "foundry",
				}
				env.config = map[string][]byte{}
				switch state {
				case "selector":
					env.get["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testACRID
				case "canonical":
					data, err := json.Marshal(acrTestBinding())
					require.NoError(t, err)
					env.config[synthesis.ACRBindingConfigKey] = data
				case "pending":
					data, err := json.Marshal(acrTestPending())
					require.NoError(t, err)
					env.config[acrPendingConfigKey] = data
				default:
					infraPath := options.Path
					if infraPath == "" {
						infraPath = onDiskInfraDir
					}
					infraPath = filepath.Join(root, infraPath)
					require.NoError(t, os.MkdirAll(infraPath, 0o750))
					template := `{"parameters":{"existingAcrResourceId":{"type":"string"},` +
						`"includeAcr":{"type":"bool","defaultValue":true}},"resources":[]}`
					params := minimalARMParametersFile(t, map[string]any{"existingAcrResourceId": testACRID})
					compiler := &stubCompiler{buildResult: bicep.BuildResult{Compiled: template}}
					if state == "ejected bicepparam" {
						compiled, err := json.Marshal(map[string]string{"templateJson": template, "parametersJson": params})
						require.NoError(t, err)
						compiler.buildParamResult.Compiled = string(compiled)
						require.NoError(t, os.WriteFile(filepath.Join(infraPath, "main.bicepparam"), []byte("using './main.bicep'"), 0o600))
					} else {
						require.NoError(t, os.WriteFile(filepath.Join(infraPath, "main.bicep"), []byte("// generated"), 0o600))
						require.NoError(t, os.WriteFile(filepath.Join(infraPath, "main.parameters.json"), []byte(params), 0o600))
					}
					p.bicepCliInstance = compiler
					p.azdClient = newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{location: "eastus2"})
				}
				beforeValues, beforeConfig := maps.Clone(env.get), maps.Clone(env.config)
				err := p.Initialize(t.Context(), root, options)
				if strings.HasPrefix(state, "ejected ") {
					require.NoError(t, err)
					_, err = p.Deploy(t.Context(), func(string) {})
					requireReuseDisabled(t, err)
					_, err = p.Preview(t.Context(), func(string) {})
				}
				requireReuseDisabled(t, err)
				require.Empty(t, env.set, "prompted values must not be persisted before the Bicep gate")
				require.Equal(t, beforeValues, env.get)
				require.Equal(t, beforeConfig, env.config)
				// #nosec G304 -- isolated test fixture.
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, raw, string(after))
			})
		}
	}

}

func preflightOnDiskProject(
	t *testing.T, route, format, id string,
) (string, *azdext.ProvisioningOptions, string) {
	t.Helper()
	root := t.TempDir()
	layer := route != "root"
	testutil.ACRParameters(t, root, layer, "json", id)
	path, module := "infra", "main"
	options := &azdext.ProvisioningOptions{Provider: FoundryProviderName}
	if layer {
		path, module = filepath.Join("infra", "foundry"), "project"
		options.Name = "foundry"
	}
	if route == "custom layer" {
		target := filepath.Join("iac", "selected")
		require.NoError(t, os.MkdirAll(filepath.Join(root, "iac"), 0o750))
		require.NoError(t, os.Rename(filepath.Join(root, path), filepath.Join(root, target)))
		for _, extension := range []string{".bicep", ".parameters.json"} {
			require.NoError(t, os.Rename(filepath.Join(root, target, module+extension),
				filepath.Join(root, target, "custom"+extension)))
		}
		manifestPath := filepath.Join(root, "azure.yaml")
		//nolint:gosec // Test-owned project fixture.
		raw, err := os.ReadFile(manifestPath)
		require.NoError(t, err)
		raw = []byte(strings.ReplaceAll(strings.ReplaceAll(string(raw),
			"infra/foundry", "iac/selected"), "module: project", "module: custom"))
		//nolint:gosec // Test-owned project fixture.
		require.NoError(t, os.WriteFile(manifestPath, raw, 0o600))
		path, module = target, "custom"
	}
	options.Path, options.Module = path, module
	extension := ".bicep"
	if format == "bicepparam" {
		extension = ".bicepparam"
		require.NoError(t, os.WriteFile(filepath.Join(root, path, module+extension),
			[]byte("using './"+module+".bicep'\n"), 0o600))
	}
	return root, options, filepath.Join(root, path, module+extension)
}

func TestACRReuseDisabledInitializeAndDestroyDoNotCompile(t *testing.T) {
	for _, route := range []string{"root", "layer", "custom layer"} {
		for _, format := range []string{"json", "bicepparam"} {
			for _, id := range []string{"", testACRID} {
				name := route + "/" + format + "/" + map[bool]string{false: "create", true: "source-only reuse"}[id != ""]
				t.Run(name, func(t *testing.T) {
					t.Setenv(synthesis.ACRReuseEnvVar, "false")
					root, options, _ := preflightOnDiskProject(t, route, format, id)
					config, cache := filepath.Join(t.TempDir(), "config"), filepath.Join(t.TempDir(), "modules")
					t.Setenv("AZD_CONFIG_DIR", config)
					t.Setenv("BICEP_CACHE_ROOT_DIRECTORY", cache)
					t.Setenv("AZD_BICEP_TOOL_PATH", filepath.Join(root, "missing-bicep"))
					groupID := "/subscriptions/sub/resourceGroups/foundry"
					env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
						envKeySubscriptionID: "sub", envKeyResourceGroup: "foundry",
						envKeyFoundryRG: "foundry", envKeyFoundryRGOwner: groupID,
					}, config: map[string][]byte{"sentinel": []byte(`{"keep":true}`)}}
					prompt := &resolveEnvStubPromptServer{location: "eastus2"}
					p := &FoundryProvisioningProvider{
						azdClient: newResolveEnvTestClient(t, env, prompt), credential: acrTestCredential{},
						resourceGroupState: func(context.Context) (map[string]*string, bool, error) {
							return nil, false, nil
						},
					}
					deletedGroup := false
					p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
						Retry: policy.RetryOptions{MaxRetries: -1},
						Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
							switch {
							case request.Method == http.MethodGet &&
								strings.HasSuffix(request.URL.Path, "/Microsoft.CognitiveServices/accounts"):
								return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{"value": []any{}})
							case request.Method == http.MethodGet && strings.EqualFold(request.URL.Path, groupID):
								return acrTestHTTPReply(t, request, http.StatusNotFound, nil)
							case request.Method == http.MethodDelete && strings.EqualFold(request.URL.Path, groupID):
								deletedGroup = true
								return acrTestHTTPReply(t, request, http.StatusNotFound, nil)
							default:
								return nil, errors.New("unexpected ARM request")
							}
						}),
					}}
					p.acrRequest = func(_ context.Context, method, path, _ string, _ any) error {
						deploymentPath := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" +
							p.deploymentName()
						require.Equal(t, deploymentPath, path)
						if method == http.MethodDelete {
							require.True(t, deletedGroup)
							return nil
						}
						require.Equal(t, http.MethodGet, method)
						return &azcore.ResponseError{StatusCode: http.StatusNotFound}
					}
					beforeValues, beforeConfig := maps.Clone(env.get), maps.Clone(env.config)
					beforeFiles := testutil.Snapshot(t, root)
					require.NoError(t, p.Initialize(t.Context(), root, options))
					result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
					require.NoError(t, err)
					require.NotNil(t, result)
					require.True(t, deletedGroup)
					require.Equal(t, 1, prompt.locationN)
					require.Empty(t, env.set)
					require.Equal(t, beforeValues, env.get)
					require.Equal(t, beforeConfig, env.config)
					require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
					require.NoDirExists(t, config)
					require.NoDirExists(t, cache)
					require.Nil(t, p.bicepCliInstance)
					require.Nil(t, p.onDiskSource)
				})
			}
		}
	}
}

func TestACRReuseDisabledDownRecoversBindingWithoutCompiler(t *testing.T) {
	for _, format := range []string{"json", "bicepparam"} {
		t.Run(format, func(t *testing.T) {
			t.Setenv(synthesis.ACRReuseEnvVar, "false")
			root, options, _ := preflightOnDiskProject(t, "root", format, "")
			t.Setenv("AZD_BICEP_TOOL_PATH", filepath.Join(root, "missing-bicep"))
			env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
				envKeySubscriptionID: "sub", envKeyResourceGroup: "foundry", envKeyLocation: "eastus2",
			}, config: map[string][]byte{"sentinel": []byte(`{"keep":true}`)}}
			p := &FoundryProvisioningProvider{
				azdClient:  newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{}),
				credential: acrOfflineCredential{},
			}
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.Equal(t, http.MethodGet, method)
				deploymentPath := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
				require.Equal(t, deploymentPath, path)
				return acrTestReply(out, map[string]any{"properties": map[string]any{
					"provisioningState": "Succeeded",
					"outputs": map[string]any{
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"value": testACRID},
						"AZURE_AI_PROJECT_ID":                  map[string]any{"value": testACRProject},
					},
					"parameters": map[string]any{"existingAcrResourceId": map[string]any{"value": testACRID}},
				}})
			}
			beforeValues, beforeConfig := maps.Clone(env.get), maps.Clone(env.config)
			beforeFiles := testutil.Snapshot(t, root)
			require.NoError(t, p.Initialize(t.Context(), root, options))
			_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			requireReuseDisabled(t, err)
			require.Nil(t, p.bicepCliInstance)
			require.Nil(t, p.onDiskSource)
			require.Empty(t, env.set)
			require.Equal(t, beforeValues, env.get)
			require.Equal(t, beforeConfig, env.config)
			require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
		})
	}
}

func TestACRReuseDisabledInitializeInstalledCompiler(t *testing.T) {
	for _, route := range []string{"root", "layer", "custom layer"} {
		for _, format := range []string{"json", "bicepparam"} {
			for _, reuse := range []bool{false, true} {
				t.Run(route+"/"+format+"/"+map[bool]string{false: "create", true: "reuse"}[reuse], func(t *testing.T) {
					t.Setenv(synthesis.ACRReuseEnvVar, "false")
					id := ""
					if reuse {
						id = testACRID
					}
					root, options, sourcePath := preflightOnDiskProject(t, route, format, id)
					testutil.StubBicepParameters(t, minimalARMParametersFile(t,
						map[string]any{"existingAcrResourceId": id}))
					if format == "json" {
						//nolint:gosec // Test-owned output.
						require.NoError(t, os.WriteFile(os.Getenv("AZD_TEST_BICEP_OUTPUT"),
							[]byte(minimalARMTemplate()), 0o600))
					}
					config, cache := filepath.Join(root, "missing-config"), filepath.Join(root, "missing-cache")
					t.Setenv("AZD_CONFIG_DIR", config)
					t.Setenv("BICEP_CACHE_ROOT_DIRECTORY", cache)
					env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
						envKeySubscriptionID: "sub", envKeyResourceGroup: "foundry", envKeyLocation: "eastus2",
					}, config: map[string][]byte{"sentinel": []byte(`{"keep":true}`)}}
					p := &FoundryProvisioningProvider{azdClient: newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{})}
					beforeValues, beforeConfig := maps.Clone(env.get), maps.Clone(env.config)
					beforeFiles := testutil.Snapshot(t, root)
					require.NoError(t, p.Initialize(t.Context(), root, options))
					require.Nil(t, p.onDiskSource)
					_, err := p.resolveTemplate(t.Context(), func(string) {})
					if reuse {
						requireReuseDisabled(t, err)
					} else {
						require.NoError(t, err)
					}
					require.NotNil(t, p.onDiskSource)
					require.Equal(t, sourcePath, p.onDiskSource.sourcePath)
					require.Equal(t, beforeValues, env.get)
					require.Equal(t, beforeConfig, env.config)
					require.Empty(t, env.set)
					require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
					require.NoDirExists(t, config)
					require.NoDirExists(t, cache)
				})
			}
		}
	}
}

func TestACRReuseCompilerSelectionPreservesNormalAndInjectedBehavior(t *testing.T) {
	for _, enabled := range []string{"false", "true"} {
		t.Setenv(synthesis.ACRReuseEnvVar, enabled)
		p := &FoundryProvisioningProvider{}
		compiler := p.bicepCli()
		require.IsType(t, &bicep.Cli{}, compiler)
		require.Same(t, compiler, p.bicepCli())
		injected := &stubCompiler{}
		p.bicepCliInstance = injected
		require.Same(t, injected, p.bicepCli())
	}
}
