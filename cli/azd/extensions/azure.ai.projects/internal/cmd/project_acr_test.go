// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"azure.ai.projects/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const testExternalRegistryID = "/subscriptions/subscription/resourceGroups/external/providers/" +
	"Microsoft.ContainerRegistry/registries/shared"

func TestProjectAddRestoresBoundSubscription(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, mode := range []string{"create", "reuse-connect"} {
		for _, saved := range []string{"", "subscription", "other"} {
			t.Run(mode+"/"+saved, func(t *testing.T) {
				root := t.TempDir()
				t.Setenv("AZD_EXEC_PROJECT_DIR", root)
				raw := []byte("name: test\ninfra:\n  provider: microsoft.foundry\nservices:\n" +
					"  project:\n    host: azure.ai.project\n")
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), raw, 0o600))
				client, project, env, _, _ := newSelfInitializingDeploymentClient(t, root)
				project.project.Infra = &azdext.InfraOptions{Provider: "microsoft.foundry"}
				project.project.Services["project"] = &azdext.ServiceConfig{Name: "project", Host: aiProjectHost}
				project.services["project"] = map[string]any{"host": aiProjectHost}
				env.values["AZURE_SUBSCRIPTION_ID"] = saved
				binding := synthesis.ACRBinding{
					Version: 1, Mode: mode, RegistryResourceID: testExternalRegistryID,
					ProjectResourceID: "/subscriptions/subscription/resourceGroups/project/providers/" +
						"Microsoft.CognitiveServices/accounts/account/projects/project",
					PrincipalID: "principal",
				}
				if mode == "create" {
					binding.RegistryResourceID = strings.Replace(testExternalRegistryID, "/external/", "/project/", 1)
				}
				data, err := json.Marshal(binding)
				require.NoError(t, err)
				env.config = map[string][]byte{synthesis.ACRBindingConfigKey: data}
				before := maps.Clone(env.values)
				action := &ProjectAddAction{
					client: client, flags: &projectAddFlags{output: "none"},
					extCtx: &azdext.ExtensionContext{Environment: "test"},
				}
				err = action.Run(t.Context())
				if saved == "other" {
					require.ErrorContains(t, err, "AZURE_SUBSCRIPTION_ID conflicts")
					assert.Equal(t, `restore AZURE_SUBSCRIPTION_ID to "subscription" to match the deployed project, `+
						"or use a new azd environment", azdext.ErrorSuggestion(err))
					require.Equal(t, before, env.values)
					require.Empty(t, env.setLog)
				} else {
					require.NoError(t, err)
					require.Equal(t, "subscription", env.values["AZURE_SUBSCRIPTION_ID"])
				}
				require.Equal(t, data, env.config[synthesis.ACRBindingConfigKey])
			})
		}
	}
}

func TestProjectACRSelectionSpecificGuidance(t *testing.T) {
	for _, tt := range []struct {
		name, manifest, want string
	}{
		{"hosting", "services:\n  project:\n    host: azure.ai.project\n    agentHosting:\n" +
			"      clusterResourceId: /subscriptions/sub/resourceGroups/aks/providers/" +
			"Microsoft.ContainerService/managedClusters/aks\n",
			"remove agentHosting"},
		{"unknown scope", "infra:\n  provider: bicep\nservices:\n  project:\n    host: azure.ai.project\n",
			"Use Foundry-only infrastructure"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := projectACRSelection([]byte(tt.manifest), t.TempDir(), "test",
				map[string]string{"AZURE_SUBSCRIPTION_ID": "subscription"},
				&resolvedProject{ACRResourceID: testExternalRegistryID}, nil)
			require.ErrorContains(t, err, tt.want)
			assert.Empty(t, azdext.ErrorSuggestion(err))
		})
	}
}

func TestProjectAddACRInvalidFlagsBeforeDaemon(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"empty", []string{"--new-project", "--acr-resource-id="}, "cannot be empty"},
		{"invalid ID", []string{"--new-project", "--acr-resource-id=shared"}, "full ARM ID"},
		{"query suffix", []string{"--new-project", "--acr-resource-id=" +
			testExternalRegistryID + "?sig=secret"}, "full ARM ID"},
		{"missing new project", []string{"--acr-resource-id=" + testExternalRegistryID}, "requires --new-project"},
		{"existing empty ID", []string{"--new-project", "--project-id=", "--acr-resource-id=" +
			testExternalRegistryID}, "--project-id"},
		{"existing empty endpoint", []string{"--new-project", "--project-endpoint=", "--acr-resource-id=" +
			testExternalRegistryID}, "--project-endpoint"},
		{"explicit force false", []string{"--new-project", "--force=false", "--acr-resource-id=" +
			testExternalRegistryID}, "--force"},
		{"Terraform", []string{"--new-project", "--infra=terraform", "--acr-resource-id=" +
			testExternalRegistryID}, "--infra=terraform"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := newProjectAddCommand(&azdext.ExtensionContext{NoPrompt: true})
			command.SetContext(t.Context())
			require.NoError(t, command.ParseFlags(tt.args))
			err := command.RunE(command, nil)
			require.ErrorContains(t, err, tt.want)
			assert.NotContains(t, err.Error(), "daemon")
		})
	}
}

func TestProjectACRStateErrorsDoNotSuggestRegistrySelection(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	const projectID = "/subscriptions/subscription/resourceGroups/project/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	binding, err := json.Marshal(synthesis.ACRBinding{
		Version: 1, Mode: "reuse-connect", RegistryResourceID: testExternalRegistryID,
		ProjectResourceID: projectID, PrincipalID: "principal",
	})
	require.NoError(t, err)
	for _, tt := range []struct {
		name, want string
		binding    []byte
		projectID  string
	}{
		{"invalid", "binding", []byte(`{"version":3}`), projectID},
		{"empty", "binding is empty", []byte{}, projectID},
		{"identity", "use a new azd environment", binding, projectID + "-other"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := &acrPreflightEnvironmentServer{
				transactionEnvironmentServer: transactionEnvironmentServer{values: map[string]string{
					"AZURE_AI_PROJECT_ID": tt.projectID, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testExternalRegistryID,
					"AZD_FOUNDRY_ACR_MODE": "reuse-connect",
				}},
				binding: tt.binding,
			}
			action := &ProjectAddAction{
				client: newProjectEnvironmentClient(t, server),
				flags:  &projectAddFlags{newProject: true},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			err := action.preflightProjectACR(t.Context(), t.TempDir())
			require.ErrorContains(t, err, tt.want)
			assert.Empty(t, azdext.ErrorSuggestion(err))
			assert.Zero(t, server.calls)
		})
	}
}

func TestProjectAddACRTerraformManifestBeforeDaemon(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AZD_EXEC_PROJECT_DIR", root)
	raw := []byte("name: test\ninfra:\n  provider: terraform\nservices: {}\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), raw, 0600))
	action := &ProjectAddAction{flags: &projectAddFlags{
		newProject: true, acrResourceIDSet: true, acrResourceID: testExternalRegistryID,
	}}
	require.ErrorContains(t, action.Run(t.Context()), "Terraform")
	assert.Nil(t, action.client)
}

func TestProjectACRManifestRequirements(t *testing.T) {
	tests := []struct {
		name string
		body string
		env  map[string]string
		want string
	}{
		{"pending project", "services:\n  project:\n    host: azure.ai.project\n", nil, ""},
		{"source build", "services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n", nil, ""},
		{"image defaults to source", "services:\n  agent:\n    host: azure.ai.agent\n    image: registry/image:v1\n",
			nil, ""},
		{"prebuilt", "services:\n  agent:\n    host: azure.ai.agent\n    image: registry/image:v1\n" +
			"    docker: {imagePassthrough: true}\n", nil, "prebuilt-image"},
		{"code", "services:\n  agent:\n    host: azure.ai.agent\n    codeConfiguration:\n      runtime: python\n",
			nil, "code"},
		{"prompt", "services:\n  agent:\n    host: azure.ai.agent\n    kind: prompt\n", nil, "non-container"},
		{"legacy prebuilt", "services:\n  agent:\n    host: azure.ai.agent\n    agents:\n" +
			"      - name: test\n        image: registry/image:v1\n" +
			"        docker: {imagePassthrough: true}\n", nil, "prebuilt-image"},
		{"conditional source disabled", "services:\n  source:\n    host: azure.ai.agent\n" +
			"    condition: ${BUILD_ENABLED}\n  prebuilt:\n    host: azure.ai.agent\n    image: registry/image:v1\n" +
			"    docker: {imagePassthrough: true}\n",
			map[string]string{"BUILD_ENABLED": "false"}, "prebuilt-image"},
		{"conditional source enabled", "services:\n  source:\n    host: azure.ai.agent\n" +
			"    condition: ${BUILD_ENABLED}\n  prebuilt:\n    host: azure.ai.agent\n    image: registry/image:v1\n" +
			"    docker: {imagePassthrough: true}\n",
			map[string]string{"BUILD_ENABLED": "true"}, ""},
		{"Terraform", "infra:\n  provider: terraform\nservices: {}\n", nil, "Terraform"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProjectACRManifest([]byte(tt.body), t.TempDir(), tt.env)
			if tt.want != "" {
				require.ErrorContains(t, err, tt.want)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestProjectACRManifestReference(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte(
		"host: azure.ai.agent\nconfig:\n  kind: hosted\n  image: registry/image:v1\n"+
			"  docker: {imagePassthrough: true}\n"), 0600))
	raw := []byte("services:\n  agent:\n    $ref: agent.yaml\n")
	// #nosec G304 -- path is within the test project.
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.ErrorContains(t, validateProjectACRManifest(raw, root, nil), "prebuilt-image")
	// #nosec G304 -- path is within the test project.
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestProjectACRSelectionOverridesUndeployedDefaults(t *testing.T) {
	old := map[string]string{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": "old-default",
		"AZD_FOUNDRY_ACR_MODE":                 "create",
		"AZURE_AI_PROJECT_ID":                  "old-project",
	}
	project := &resolvedProject{ACRResourceID: testExternalRegistryID}
	plan := planProjectEnvironment(old, projectModeNew, project, true)
	effective := applyProjectEnvironmentPlan(old, plan)
	assert.Equal(t, testExternalRegistryID, effective["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
	assert.Equal(t, "reuse-connect", effective["AZD_FOUNDRY_ACR_MODE"])
	assert.NotContains(t, effective, "AZURE_AI_PROJECT_ID")
	assert.NotContains(t, plan.Unsets, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
	assert.NotContains(t, plan.Unsets, "AZD_FOUNDRY_ACR_MODE")
}

func TestProjectACRSelectionDoesNotAdoptExistingProjectOutputs(t *testing.T) {
	for _, mode := range []string{"already-connected", "none", "create", "reuse-connect"} {
		t.Run(mode, func(t *testing.T) {
			values := map[string]string{
				"USE_EXISTING_AI_PROJECT": "true",
				"AZURE_AI_PROJECT_ID": "/subscriptions/subscription/resourceGroups/existing/" +
					"providers/Microsoft.CognitiveServices/accounts/account/projects/project",
				"FOUNDRY_PROJECT_ENDPOINT":             "https://account.services.ai.azure.com/api/projects/project",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testExternalRegistryID,
				"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "shared.azurecr.io",
				"AZD_FOUNDRY_ACR_MODE":                 mode,
			}
			before := maps.Clone(values)
			project := &resolvedProject{Mode: projectModeNew}
			selected, err := projectACRSelection(nil, t.TempDir(), "test", values, project, nil)
			require.NoError(t, err)
			assert.Empty(t, selected)
			assert.Empty(t, project.ACRResourceID)
			assert.Equal(t, before, values)
		})
	}
}

func TestProjectAddACRPendingSelection(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, mode := range []string{"create", "none", "reuse-connect"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			client, projectServer, environment, _, _ := newSelfInitializingDeploymentClient(t, root)
			environment.values["AZURE_AI_PROJECT_ID"] = "undeployed-default"
			environment.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testExternalRegistryID + "default"
			environment.values["AZD_FOUNDRY_ACR_MODE"] = mode
			action := &ProjectAddAction{
				client: client,
				flags: &projectAddFlags{
					newProject: true, acrResourceIDSet: true, acrResourceID: testExternalRegistryID,
					noPrompt: true, output: "none",
				},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			require.NoError(t, action.Run(t.Context()))
			assert.Equal(t, testExternalRegistryID, environment.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
			assert.Equal(t, "reuse-connect", environment.values["AZD_FOUNDRY_ACR_MODE"])
			assert.Equal(t, "false", environment.values["USE_EXISTING_AI_PROJECT"])
			assert.Empty(t, environment.values["AZURE_AI_PROJECT_ID"])
			require.Len(t, projectServer.project.Services, 1)
			for _, service := range projectServer.project.Services {
				assert.Equal(t, aiProjectHost, service.GetHost())
			}
		})
	}
}

func TestProjectAddACREnvironmentSelectionWithConfiguredName(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, key := range []string{"AZURE_AI_PROJECT_NAME", "AZURE_AI_ACCOUNT_NAME"} {
		for _, mode := range []string{"", "reuse-connect"} {
			for _, invalidScope := range []bool{false, true} {
				t.Run(key+"/"+mode+"/"+strconv.FormatBool(invalidScope), func(t *testing.T) {
					root := t.TempDir()
					t.Setenv("AZD_EXEC_PROJECT_DIR", root)
					client, project, env, _, workflow := newSelfInitializingDeploymentClient(t, root)
					selectedID := testExternalRegistryID
					if invalidScope {
						selectedID = strings.Replace(selectedID, "/external/", "/rg-test/", 1)
					}
					env.values[key] = "configured-name"
					env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = selectedID
					env.values["AZD_FOUNDRY_ACR_MODE"] = mode
					before := maps.Clone(env.values)
					action := &ProjectAddAction{
						client: client,
						flags:  &projectAddFlags{newProject: true, noPrompt: true, output: "none"},
						extCtx: &azdext.ExtensionContext{Environment: "test"},
					}
					err := action.Run(t.Context())
					if invalidScope {
						require.ErrorContains(t, err, "azd down deletes")
						assert.Equal(t, before, env.values)
						assert.Empty(t, env.setLog)
						assert.Empty(t, workflow.args)
						assert.False(t, project.providerSet)
						assert.Empty(t, project.project.Services)
						assert.NoFileExists(t, filepath.Join(root, "azure.yaml"))
						return
					}
					require.NoError(t, err)
					assert.Equal(t, selectedID, env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
					assert.Equal(t, "reuse-connect", env.values["AZD_FOUNDRY_ACR_MODE"])
					assert.Empty(t, env.values["AZURE_AI_PROJECT_ID"])
					assert.Len(t, project.project.Services, 1)
				})
			}
		}
	}
}

func TestProjectAddACRPlannedScopeBeforeScaffolding(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, layer := range []bool{false, true} {
		t.Run(map[bool]string{false: "root", true: "layer"}[layer], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			group := "rg-test"
			if layer {
				group += "-foundry"
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
					"name: test\ninfra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n"), 0600))
			}
			client, projectServer, environment, _, workflow := newSelfInitializingDeploymentClient(t, root)
			before := maps.Clone(environment.values)
			selector := "/subscriptions/subscription/resourceGroups/" + group +
				"/providers/Microsoft.ContainerRegistry/registries/shared"
			action := &ProjectAddAction{
				client: client,
				flags: &projectAddFlags{
					newProject: true, acrResourceIDSet: true, acrResourceID: selector, noPrompt: true, output: "none",
				},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			require.ErrorContains(t, action.Run(t.Context()), "azd down deletes")
			assert.Empty(t, workflow.args)
			assert.False(t, projectServer.providerSet)
			assert.Empty(t, projectServer.project.Services)
			assert.Equal(t, before, environment.values)
			assert.Empty(t, environment.setLog)
			assert.NoDirExists(t, filepath.Join(root, ".azure"))
			if !layer {
				assert.NoFileExists(t, filepath.Join(root, "azure.yaml"))
			}
		})
	}
}

func TestProjectAddACREnvironmentInputBeforeScaffolding(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, layer := range []bool{false, true} {
		t.Run(strconv.FormatBool(layer), func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			group := "rg-test"
			if layer {
				group += "-foundry"
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
					"name: test\ninfra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n"), 0o600))
			}
			client, project, env, _, workflow := newSelfInitializingDeploymentClient(t, root)
			env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = "/subscriptions/subscription/resourceGroups/" + group +
				"/providers/Microsoft.ContainerRegistry/registries/shared"
			before := maps.Clone(env.values)
			action := &ProjectAddAction{
				client: client,
				flags:  &projectAddFlags{newProject: true, noPrompt: true, output: "none"},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			require.ErrorContains(t, action.Run(t.Context()), "azd down deletes")
			assert.Empty(t, workflow.args)
			assert.False(t, project.providerSet)
			assert.Empty(t, project.project.Services)
			assert.Empty(t, env.setLog)
			assert.Equal(t, before, env.values)
			assert.NoDirExists(t, filepath.Join(root, ".azure"))
			if !layer {
				assert.NoFileExists(t, filepath.Join(root, "azure.yaml"))
			}
		})
	}
}

func TestProjectAddExplicitEnvironmentInEmptyDirectory(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, selector := range []bool{false, true} {
		t.Run(strconv.FormatBool(selector), func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			client, project, env, _, workflow := newSelfInitializingDeploymentClient(t, root)
			env.projectRoot = root
			action := &ProjectAddAction{
				client: client,
				flags: &projectAddFlags{
					newProject: true, noPrompt: true, output: "none",
					acrResourceIDSet: selector,
				},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			if selector {
				action.flags.acrResourceID = testExternalRegistryID
			}
			require.NoError(t, action.Run(t.Context()))
			require.NotEmpty(t, workflow.args)
			assert.Contains(t, workflow.args[0], "--environment")
			assert.Contains(t, workflow.args[0], "test")
			assert.FileExists(t, filepath.Join(root, "azure.yaml"))
			require.Len(t, project.project.Services, 1)
			if selector {
				assert.Equal(t, testExternalRegistryID, env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
				assert.Equal(t, "reuse-connect", env.values["AZD_FOUNDRY_ACR_MODE"])
			}
		})
	}
}

func TestProjectAddACRRejectsCrossSubscriptionBeforeScaffolding(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, selectorFlag := range []bool{false, true} {
		for _, layer := range []bool{false, true} {
			t.Run(strconv.FormatBool(selectorFlag)+"/"+strconv.FormatBool(layer), func(t *testing.T) {
				root := t.TempDir()
				t.Setenv("AZD_EXEC_PROJECT_DIR", root)
				if layer {
					require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
						"name: test\ninfra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n"), 0o600))
				}
				client, project, env, _, workflow := newSelfInitializingDeploymentClient(t, root)
				selector := strings.Replace(testExternalRegistryID, "/subscription/", "/other-subscription/", 1)
				flags := &projectAddFlags{newProject: true, noPrompt: true, output: "none"}
				if selectorFlag {
					flags.acrResourceIDSet, flags.acrResourceID = true, selector
				} else {
					env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = selector
				}
				before := maps.Clone(env.values)
				action := &ProjectAddAction{
					client: client, flags: flags,
					extCtx: &azdext.ExtensionContext{Environment: "test"},
				}
				require.ErrorContains(t, action.Run(t.Context()), "same subscription")
				assert.Empty(t, workflow.args)
				assert.False(t, project.providerSet)
				assert.Empty(t, project.project.Services)
				assert.Empty(t, env.setLog)
				assert.Equal(t, before, env.values)
				assert.NoDirExists(t, filepath.Join(root, ".azure"))
				if !layer {
					assert.NoFileExists(t, filepath.Join(root, "azure.yaml"))
				}
			})
		}
	}
}

func TestProjectAddACRValidatesResolvedSubscription(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, selectorFlag := range []bool{false, true} {
		for _, layer := range []bool{false, true} {
			for _, subscription := range []string{"subscription", "other-subscription"} {
				name := strconv.FormatBool(selectorFlag) + "/" + strconv.FormatBool(layer) + "/" + subscription
				t.Run(name, func(t *testing.T) {
					root := t.TempDir()
					t.Setenv("AZD_EXEC_PROJECT_DIR", root)
					raw := "name: test\nservices: {}\n"
					if layer {
						raw += "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n"
					}
					path := filepath.Join(root, "azure.yaml")
					require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
					prompt := &azureContextPromptServer{subscription: &azdext.Subscription{
						Id: subscription, UserTenantId: "user-tenant",
					}}
					client, project, env, _, _ := newSelfInitializingDeploymentClient(t, root, prompt)
					delete(env.values, "AZURE_SUBSCRIPTION_ID")
					flags := &projectAddFlags{newProject: true, output: "none"}
					if selectorFlag {
						flags.acrResourceIDSet, flags.acrResourceID = true, testExternalRegistryID
					} else {
						env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testExternalRegistryID
					}
					before := maps.Clone(env.values)
					action := &ProjectAddAction{
						client: client, flags: flags,
						extCtx: &azdext.ExtensionContext{Environment: "test"},
					}
					err := action.Run(t.Context())
					assert.Equal(t, 1, prompt.subscriptionCalls)
					if subscription == "subscription" {
						require.NoError(t, err)
						assert.Equal(t, subscription, env.values["AZURE_SUBSCRIPTION_ID"])
						assert.Equal(t, testExternalRegistryID, env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
						assert.Equal(t, "reuse-connect", env.values["AZD_FOUNDRY_ACR_MODE"])
						return
					}
					require.ErrorContains(t, err, "same subscription")
					assert.False(t, project.providerSet)
					assert.Empty(t, project.project.Services)
					assert.Empty(t, env.setLog)
					assert.Equal(t, before, env.values)
					// #nosec G304 -- file created in t.TempDir.
					after, readErr := os.ReadFile(path)
					require.NoError(t, readErr)
					assert.Equal(t, raw, string(after))
				})
			}
		}
	}
}

type projectACRPlanningPromptServer struct {
	azureContextPromptServer
	root                     string
	subscriptionError        error
	scaffoldedAtSubscription bool
}

func (s *projectACRPlanningPromptServer) PromptSubscription(
	ctx context.Context, request *azdext.PromptSubscriptionRequest,
) (*azdext.PromptSubscriptionResponse, error) {
	exists, err := projectFileExists(s.root)
	if err != nil {
		return nil, err
	}
	s.scaffoldedAtSubscription = exists
	response, err := s.azureContextPromptServer.PromptSubscription(ctx, request)
	if s.subscriptionError != nil {
		return nil, s.subscriptionError
	}
	return response, err
}

func TestProjectAddACRPlansSubscriptionBeforeEmptyDirectorySetup(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, tt := range []struct {
		name, environment, subscription, group, wantError string
		cancelled, noPrompt, withoutSelector              bool
	}{
		{name: "explicit environment mismatch", environment: "dev", subscription: "other",
			wantError: "same subscription"},
		{name: "derived environment mismatch", subscription: "other", wantError: "same subscription"},
		{name: "cancelled selection", environment: "dev", cancelled: true, wantError: "cancelled"},
		{name: "planned teardown group", environment: "dev", group: "rg-dev", wantError: "resource-group"},
		{name: "matching explicit environment", environment: "dev", subscription: "subscription"},
		{name: "matching derived environment", subscription: "subscription"},
		{name: "headless missing context defers", environment: "dev", noPrompt: true},
		{name: "ordinary setup retains order", environment: "dev", subscription: "other", withoutSelector: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			prompt := &projectACRPlanningPromptServer{
				root: root,
				azureContextPromptServer: azureContextPromptServer{
					subscription: &azdext.Subscription{
						Id: tt.subscription, TenantId: "resource-tenant", UserTenantId: "access-tenant",
					},
					location: &azdext.Location{Name: "eastus2"},
				},
			}
			if tt.cancelled {
				prompt.subscriptionError = status.Error(codes.Canceled, "subscription selection cancelled")
			}
			client, project, env, _, workflow := newSelfInitializingDeploymentClient(t, root, prompt)
			env.projectRoot = root
			env.current = "unchanged"
			if tt.environment == "" {
				env.current = ""
			}
			env.values = map[string]string{"KEEP": "unchanged"}
			env.config = map[string][]byte{"keep": []byte(`"unchanged"`)}
			beforeValues := maps.Clone(env.values)
			beforeConfig := maps.Clone(env.config)
			beforeProject := proto.Clone(project.project)
			beforeCurrent := env.current
			flags := &projectAddFlags{newProject: true, output: "none", noPrompt: tt.noPrompt}
			if !tt.withoutSelector {
				flags.acrResourceIDSet, flags.acrResourceID = true, testExternalRegistryID
				if tt.group != "" {
					flags.acrResourceID = strings.Replace(flags.acrResourceID, "/external/", "/"+tt.group+"/", 1)
				}
			}
			action := &ProjectAddAction{
				client: client, flags: flags,
				extCtx: &azdext.ExtensionContext{Environment: tt.environment},
			}
			err := action.Run(t.Context())
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				assert.Empty(t, workflow.args)
				assert.Empty(t, env.setLog)
				assert.Equal(t, beforeValues, env.values)
				assert.Equal(t, beforeConfig, env.config)
				assert.Equal(t, beforeCurrent, env.current)
				assert.True(t, proto.Equal(beforeProject, project.project))
				assert.Empty(t, project.services)
				assert.False(t, project.providerSet)
				assert.False(t, project.deploymentSet)
				assert.False(t, prompt.scaffoldedAtSubscription)
				assert.Zero(t, prompt.locationCalls)
				assert.NoFileExists(t, filepath.Join(root, "azure.yaml"))
				entries, readErr := os.ReadDir(root)
				require.NoError(t, readErr)
				assert.Empty(t, entries)
				wantPrompts := 1
				if tt.group != "" {
					wantPrompts = 0
				}
				assert.Equal(t, wantPrompts, prompt.subscriptionCalls)
				return
			}
			require.NoError(t, err)
			require.Len(t, workflow.args, 1)
			name := tt.environment
			if name == "" {
				name = deriveProjectEnvironmentName(root)
			}
			assert.Contains(t, workflow.args[0], name)
			assert.Equal(t, name, env.current)
			assert.Equal(t, "unchanged", env.values["KEEP"])
			assert.Equal(t, beforeConfig, env.config)
			assert.Len(t, project.project.Services, 1)
			assert.FileExists(t, filepath.Join(root, "azure.yaml"))
			if tt.noPrompt {
				assert.Zero(t, prompt.subscriptionCalls)
				assert.Zero(t, prompt.locationCalls)
				assert.Empty(t, env.values["AZURE_SUBSCRIPTION_ID"])
			} else {
				assert.Equal(t, 1, prompt.subscriptionCalls)
				assert.Equal(t, 1, prompt.locationCalls)
				assert.Equal(t, tt.withoutSelector, prompt.scaffoldedAtSubscription)
				assert.Equal(t, tt.subscription, env.values["AZURE_SUBSCRIPTION_ID"])
				assert.Equal(t, "access-tenant", env.values["AZURE_TENANT_ID"])
				scope := prompt.locationRequest.GetAzureContext().GetScope()
				assert.Equal(t, tt.subscription, scope.GetSubscriptionId())
				assert.Equal(t, "access-tenant", scope.GetTenantId())
			}
			if !tt.withoutSelector {
				assert.Equal(t, testExternalRegistryID, env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
				assert.Equal(t, "reuse-connect", env.values["AZD_FOUNDRY_ACR_MODE"])
			}
			assert.NoDirExists(t, filepath.Join(root, ".azure"))
		})
	}
}

func TestProjectACRPreflightDoesNotMaskEnvironmentFailures(t *testing.T) {
	for _, tt := range []struct {
		name, message string
		code          codes.Code
		project       bool
		environment   bool
	}{
		{name: "transport", code: codes.Unavailable, message: "environment service unavailable"},
		{name: "unknown", code: codes.Unknown, message: "failed to read environment configuration"},
		{name: "existing project", code: codes.Unknown,
			message: "no project exists; to create a new project, run `azd init`", project: true},
		{name: "existing environment", code: codes.Unknown,
			message: "no project exists; to create a new project, run `azd init`", environment: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			if tt.project {
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0o600))
			}
			if tt.environment {
				require.NoError(t, os.MkdirAll(filepath.Join(root, ".azure", "test"), 0o750))
			}
			client, project, env, _, workflow := newSelfInitializingDeploymentClient(t, root)
			env.readErr = status.Error(tt.code, tt.message)
			before := maps.Clone(env.values)
			action := &ProjectAddAction{
				client: client,
				flags:  &projectAddFlags{newProject: true, noPrompt: true, output: "none"},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			require.ErrorContains(t, action.Run(t.Context()), tt.message)
			assert.Empty(t, workflow.args)
			assert.False(t, project.providerSet)
			assert.Empty(t, env.setLog)
			assert.Equal(t, before, env.values)
		})
	}
}

func TestProjectACRPreflightCurrentEnvironment(t *testing.T) {
	for _, tt := range []struct {
		name, message string
		code          codes.Code
		project       bool
		wantError     bool
	}{
		{name: "empty current environment"},
		{name: "not found", code: codes.NotFound, message: "environment not found"},
		{name: "no default", code: codes.Unknown, message: "default environment not found"},
		{name: "no project", code: codes.Unknown,
			message: "no project exists; to create a new project, run `azd init`"},
		{name: "unavailable", code: codes.Unavailable, message: "environment service unavailable", wantError: true},
		{name: "corrupt", code: codes.Unknown, message: "reading default environment: invalid JSON", wantError: true},
		{name: "unrelated absence", code: codes.Unknown, message: "environment not found in corrupt metadata", wantError: true},
		{name: "canceled", code: codes.Canceled, message: "environment read canceled", wantError: true},
		{name: "existing project", code: codes.Unknown, project: true, wantError: true,
			message: "no project exists; to create a new project, run `azd init`"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			raw := []byte("name: test\n")
			if tt.project {
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), raw, 0o600))
			}
			client, project, env, _, workflow := newSelfInitializingDeploymentClient(t, root)
			env.current = ""
			if tt.code != codes.OK {
				env.currentErr = status.Error(tt.code, tt.message)
			}
			before := maps.Clone(env.values)
			action := &ProjectAddAction{
				client: client,
				flags:  &projectAddFlags{newProject: true, noPrompt: true, output: "none"},
			}
			err := action.Run(t.Context())
			if !tt.wantError {
				require.NoError(t, err)
				assert.Len(t, project.project.Services, 1)
				return
			}
			require.ErrorContains(t, err, tt.message)
			assert.Empty(t, workflow.args)
			assert.False(t, project.providerSet)
			assert.Empty(t, project.project.Services)
			assert.Empty(t, env.setLog)
			assert.Equal(t, before, env.values)
			assert.NoDirExists(t, filepath.Join(root, ".azure"))
			if tt.project {
				//nolint:gosec // Test-owned path.
				persisted, readErr := os.ReadFile(filepath.Join(root, "azure.yaml"))
				require.NoError(t, readErr)
				assert.Equal(t, raw, persisted)
			} else {
				assert.NoFileExists(t, filepath.Join(root, "azure.yaml"))
			}
		})
	}
}

func TestProjectAddAlreadyConnectedRegistryOwnershipOutput(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	for _, mode := range []string{"already-connected", ""} {
		for _, hasRegistry := range []bool{false, true} {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(mode+"/"+strconv.FormatBool(hasRegistry)+"/"+format, func(t *testing.T) {
					root := t.TempDir()
					t.Setenv("AZD_EXEC_PROJECT_DIR", root)
					raw := "name: test\nservices:\n  project:\n    host: azure.ai.project\n    endpoint: " + endpoint + "\n"
					require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(raw), 0o600))
					client, project, env, _, _ := newSelfInitializingDeploymentClient(t, root)
					project.project.Services["project"] = &azdext.ServiceConfig{Name: "project", Host: aiProjectHost}
					project.services["project"] = map[string]any{"host": aiProjectHost, "endpoint": endpoint}
					maps.Copy(env.values, map[string]string{
						"FOUNDRY_PROJECT_ENDPOINT": endpoint, "AZURE_AI_PROJECT_NAME": "project",
						"USE_EXISTING_AI_PROJECT": "true", "AZD_FOUNDRY_ACR_MODE": mode,
					})
					if hasRegistry {
						env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testExternalRegistryID
						env.values["AZURE_CONTAINER_REGISTRY_ENDPOINT"] = "shared.azurecr.io"
						env.values["AZURE_AI_PROJECT_ACR_CONNECTION_NAME"] = "shared-conn"
					}
					var output, diagnostics bytes.Buffer
					action := &ProjectAddAction{
						client: client, stdout: &output, stderr: &diagnostics,
						flags:  &projectAddFlags{projectEndpoint: endpoint, noPrompt: true, output: format},
						extCtx: &azdext.ExtensionContext{Environment: "test"},
					}
					require.NoError(t, action.Run(t.Context()))
					assert.Empty(t, diagnostics.String())
					assert.Equal(t, mode, env.values["AZD_FOUNDRY_ACR_MODE"])
					switch format {
					case "none":
						assert.Empty(t, output.String())
					case "json":
						var result map[string]any
						require.NoError(t, json.Unmarshal(output.Bytes(), &result))
						assert.Equal(t, float64(1), result["schemaVersion"])
						assert.Equal(t, projectOutputProducerVersion(), result["producerVersion"])
						assert.Equal(t, "project", result["serviceName"])
						assert.Equal(t, string(projectModeExistingEndpoint), result["mode"])
						assert.Equal(t, "unchanged", result["mutation"])
						assert.Equal(t, endpoint, result["endpoint"])
						assert.NotContains(t, result, "resourceId")
						if hasRegistry {
							assert.Equal(t, map[string]any{
								"resourceId": testExternalRegistryID, "externallyOwned": true,
							}, result["containerRegistry"])
						} else {
							assert.NotContains(t, result, "containerRegistry")
						}
					default:
						if hasRegistry {
							assert.Contains(t, output.String(), testExternalRegistryID)
							assert.Contains(t, output.String(), "externally owned; retained by azd down")
							assert.NotContains(t, output.String(), "managed by azd")
						} else {
							assert.NotContains(t, output.String(), "Container registry:")
						}
					}
				})
			}
		}
	}
}

func TestProjectAddEnvironmentRegistryOwnershipOutput(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, managed := range []bool{false, true} {
		for _, format := range []string{"default", "json"} {
			t.Run(strconv.FormatBool(managed)+"/"+format, func(t *testing.T) {
				root := t.TempDir()
				t.Setenv("AZD_EXEC_PROJECT_DIR", root)
				client, project, env, _, _ := newSelfInitializingDeploymentClient(t, root)
				id := testExternalRegistryID
				if managed {
					id = "/subscriptions/subscription/resourceGroups/rg-test/" +
						"providers/Microsoft.ContainerRegistry/registries/shared"
					env.values["AZURE_AI_PROJECT_ID"] = "/subscriptions/subscription/resourceGroups/rg-test/" +
						"providers/Microsoft.CognitiveServices/accounts/account/projects/project"
					env.values["AZURE_RESOURCE_GROUP"] = "rg-test"
					env.values["AZURE_CONTAINER_REGISTRY_ENDPOINT"] = "shared.azurecr.io"
				} else {
					require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
						"name: test\ninfra:\n  provider: microsoft.foundry\n"+
							"services:\n  project:\n    host: azure.ai.project\n"), 0o600))
					project.project.Infra = &azdext.InfraOptions{Provider: provisioningFoundryProvider}
					project.project.Services["project"] = &azdext.ServiceConfig{Name: "project", Host: aiProjectHost}
					project.services["project"] = map[string]any{"host": aiProjectHost}
				}
				env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = id
				var output, diagnostics bytes.Buffer
				action := &ProjectAddAction{
					client: client,
					flags:  &projectAddFlags{newProject: true, noPrompt: true, output: format},
					extCtx: &azdext.ExtensionContext{Environment: "test"},
					stdout: &output, stderr: &diagnostics,
				}
				require.NoError(t, action.Run(t.Context()))
				assert.Empty(t, diagnostics.String())
				assert.Equal(t, id, env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
				if !managed {
					assert.Equal(t, "reuse-connect", env.values["AZD_FOUNDRY_ACR_MODE"])
				}
				if format == "json" {
					var result projectAddOutput
					require.NoError(t, json.Unmarshal(output.Bytes(), &result))
					require.NotNil(t, result.ContainerRegistry)
					assert.Equal(t, id, result.ContainerRegistry.ResourceID)
					assert.Equal(t, !managed, result.ContainerRegistry.ExternallyOwned)
					if !managed {
						assert.Equal(t, "updated", result.Mutation)
					}
				} else {
					assert.Contains(t, output.String(), id)
					label := "externally owned"
					if managed {
						label = "managed by azd"
					}
					assert.Contains(t, output.String(), label)
				}
			})
		}
	}
}

func TestProjectACRSelectionRollback(t *testing.T) {
	for _, failAt := range []int{0, 1, 2, 3} {
		t.Run(strconv.Itoa(failAt), func(t *testing.T) {
			old := map[string]string{
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": "old-default",
				"AZD_FOUNDRY_ACR_MODE":                 "create",
				"USE_EXISTING_AI_PROJECT":              "true",
			}
			server := &transactionEnvironmentServer{values: maps.Clone(old), failAt: failAt}
			client := newProjectEnvironmentClient(t, server)
			restore, _, err := reconcileProjectEnvironmentWithRollback(
				t.Context(), client, "test", projectModeNew,
				&resolvedProject{ACRResourceID: testExternalRegistryID}, false,
			)
			if failAt == 0 {
				require.NoError(t, err)
				assert.Equal(t, testExternalRegistryID, server.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
				require.NoError(t, restore())
			} else {
				require.Error(t, err)
			}
			assert.Equal(t, old, server.values)
		})
	}
}

func TestProjectAddACRDeployedIdentity(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	const projectID = "/subscriptions/subscription/resourceGroups/project/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same binding", true: "different binding"}[changed], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			raw := []byte("name: test\ninfra:\n  provider: microsoft.foundry\nservices:\n" +
				"  project:\n    host: azure.ai.project\n  agent:\n    host: azure.ai.agent\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), raw, 0600))
			client, projectServer, environment, _, _ := newSelfInitializingDeploymentClient(t, root)
			projectServer.project.Infra = &azdext.InfraOptions{Provider: "microsoft.foundry"}
			projectServer.project.Services["project"] = &azdext.ServiceConfig{
				Name: "project", Host: aiProjectHost,
			}
			projectServer.services["project"] = map[string]any{"host": aiProjectHost}
			maps.Copy(environment.values, map[string]string{
				"AZURE_AI_PROJECT_ID": projectID, "AZURE_RESOURCE_GROUP": "project",
				"AZURE_AI_ACCOUNT_NAME": "account", "AZURE_AI_PROJECT_NAME": "project",
				"FOUNDRY_PROJECT_ENDPOINT": "https://account.services.ai.azure.com/api/projects/project",
				"USE_EXISTING_AI_PROJECT":  "false", "AZD_FOUNDRY_ACR_MODE": "reuse-connect",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testExternalRegistryID,
				"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "shared.azurecr.io",
				"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": "registry-connection",
				"AZD_FOUNDRY_ACR_PULL_ASSIGNED":        "true",
			})
			binding, err := json.Marshal(synthesis.ACRBinding{
				Version: 1, Mode: "reuse-connect", RegistryResourceID: testExternalRegistryID,
				ProjectResourceID: projectID, PrincipalID: "principal",
			})
			require.NoError(t, err)
			environment.config = map[string][]byte{synthesis.ACRBindingConfigKey: binding}
			before := maps.Clone(environment.values)
			selector := testExternalRegistryID
			if changed {
				selector += "different"
			}
			action := &ProjectAddAction{
				client: client,
				flags: &projectAddFlags{
					newProject: true, acrResourceIDSet: true, acrResourceID: selector,
					noPrompt: true, output: "none",
				},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			err = action.Run(t.Context())
			if changed {
				require.ErrorContains(t, err, "migration")
				assert.Empty(t, environment.setLog)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, before, environment.values)
			assert.Equal(t, binding, environment.config[synthesis.ACRBindingConfigKey])
			assert.False(t, projectServer.providerSet)
			// #nosec G304 -- path is within the test project.
			persisted, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			assert.Equal(t, raw, persisted)
		})
	}
}

func TestProjectAddCanonicalBindingWithoutLegacyAliases(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	const projectID = "/subscriptions/subscription/resourceGroups/project/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	for _, tt := range []struct {
		mode        string
		explicitNew bool
		aliases     bool
	}{
		{mode: "reuse-connect"},
		{mode: "reuse-connect", explicitNew: true},
		{mode: "reuse-connect", aliases: true},
		{mode: "reuse-connect", explicitNew: true, aliases: true},
		{mode: "create"},
		{mode: "create", explicitNew: true},
		{mode: "create", aliases: true},
		{mode: "create", explicitNew: true, aliases: true},
	} {
		t.Run(tt.mode+"/"+strconv.FormatBool(tt.explicitNew)+"/aliases="+strconv.FormatBool(tt.aliases), func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			raw := []byte("name: test\ninfra:\n  provider: microsoft.foundry\nservices:\n" +
				"  project:\n    host: azure.ai.project\n")
			path := filepath.Join(root, "azure.yaml")
			require.NoError(t, os.WriteFile(path, raw, 0o600))
			client, project, env, _, _ := newSelfInitializingDeploymentClient(t, root)
			project.project.Infra = &azdext.InfraOptions{Provider: "microsoft.foundry"}
			project.project.Services["project"] = &azdext.ServiceConfig{Name: "project", Host: aiProjectHost}
			project.services["project"] = map[string]any{"host": aiProjectHost}
			maps.Copy(env.values, map[string]string{
				"AZURE_RESOURCE_GROUP": "project", "AZURE_AI_ACCOUNT_NAME": "account",
				"AZURE_AI_PROJECT_NAME": "project", "USE_EXISTING_AI_PROJECT": "false",
				"AZURE_CONTAINER_REGISTRY_ENDPOINT": "shared.azurecr.io",
			})
			registryID := testExternalRegistryID
			if tt.mode == "create" {
				registryID = strings.Replace(registryID, "/external/", "/project/", 1)
			}
			if tt.aliases {
				env.values["AZURE_AI_PROJECT_ID"] = projectID
				env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = registryID
			}
			binding, err := json.Marshal(synthesis.ACRBinding{
				Version: 1, Mode: tt.mode, RegistryResourceID: registryID,
				ProjectResourceID: projectID, PrincipalID: "principal",
			})
			require.NoError(t, err)
			env.config = map[string][]byte{synthesis.ACRBindingConfigKey: binding}
			before := maps.Clone(env.values)
			var output bytes.Buffer
			flags := &projectAddFlags{newProject: tt.explicitNew, noPrompt: true, output: "json"}
			action := &ProjectAddAction{
				client: client, flags: flags, stdout: &output,
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			require.NoError(t, action.Run(t.Context()))
			require.NotNil(t, flags.deployedBinding)
			assert.Equal(t, projectID, flags.preserveProjectID)
			for key, value := range before {
				assert.Equal(t, value, env.values[key], key)
			}
			if tt.mode == "reuse-connect" {
				assert.Equal(t, registryID, env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
				assert.Equal(t, tt.mode, env.values["AZD_FOUNDRY_ACR_MODE"])
			} else {
				if tt.aliases {
					assert.Equal(t, registryID, env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
				} else {
					assert.Empty(t, env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
				}
				assert.Empty(t, env.values["AZD_FOUNDRY_ACR_MODE"])
			}
			assert.Equal(t, binding, env.config[synthesis.ACRBindingConfigKey])
			assert.False(t, project.providerSet)
			var result projectAddOutput
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			assert.Equal(t, string(projectModeNew), result.Mode)
			assert.Equal(t, projectID, result.ResourceID)
			require.NotNil(t, result.ContainerRegistry)
			assert.Equal(t, registryID, result.ContainerRegistry.ResourceID)
			assert.Equal(t, tt.mode == "reuse-connect", result.ContainerRegistry.ExternallyOwned)
			//nolint:gosec // Test-owned path.
			persisted, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			assert.Equal(t, raw, persisted)
		})
	}
}

func TestProjectAddRejectsOwnedProjectAdoptionBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	const projectID = "/subscriptions/subscription/resourceGroups/project/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	for _, legacy := range []bool{false, true} {
		for _, endpoint := range []bool{false, true} {
			for _, targetName := range []string{"project", "other"} {
				t.Run(strconv.FormatBool(legacy)+"/endpoint="+strconv.FormatBool(endpoint)+"/target="+targetName, func(t *testing.T) {
					root := t.TempDir()
					t.Setenv("AZD_EXEC_PROJECT_DIR", root)
					client, project, env, _, workflow := newSelfInitializingDeploymentClient(t, root)
					maps.Copy(env.values, map[string]string{
						"AZURE_AI_PROJECT_ID": projectID, "USE_EXISTING_AI_PROJECT": "false",
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testExternalRegistryID,
						"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "shared.azurecr.io", "AZD_FOUNDRY_ACR_MODE": "reuse-connect",
					})
					if !legacy {
						binding, err := json.Marshal(synthesis.ACRBinding{
							Version: 1, Mode: "reuse-connect", RegistryResourceID: testExternalRegistryID,
							ProjectResourceID: projectID, PrincipalID: "principal",
						})
						require.NoError(t, err)
						env.config = map[string][]byte{synthesis.ACRBindingConfigKey: binding}
					}
					before, beforeConfig := maps.Clone(env.values), maps.Clone(env.config)
					targetID := strings.TrimSuffix(projectID, "/project") + "/" + targetName
					flags := &projectAddFlags{noPrompt: true, output: "none", projectID: targetID}
					if endpoint {
						flags.projectID = ""
						flags.projectEndpoint = "https://account.services.ai.azure.com/api/projects/" + targetName
					}
					action := &ProjectAddAction{client: client, flags: flags,
						extCtx: &azdext.ExtensionContext{Environment: "test"}}
					err := action.Run(t.Context())
					if targetName == "project" {
						require.ErrorContains(t, err, "cannot adopt")
					} else {
						require.ErrorContains(t, err, "cannot replace")
						assert.Contains(t, azdext.ErrorSuggestion(err), "retain the deployed project")
						assert.Contains(t, azdext.ErrorSuggestion(err), "--project-id")
						assert.Contains(t, azdext.ErrorSuggestion(err), "--project-endpoint")
					}
					assert.Contains(t, azdext.ErrorSuggestion(err), "new azd environment")
					assert.NotContains(t, azdext.ErrorSuggestion(err), "azd down")
					assert.Equal(t, before, env.values)
					assert.Equal(t, beforeConfig, env.config)
					assert.Empty(t, env.setLog)
					assert.Empty(t, workflow.args)
					assert.False(t, project.providerSet)
					assert.NoFileExists(t, filepath.Join(root, "azure.yaml"))
					assert.NoDirExists(t, filepath.Join(root, ".azure"))
				})
			}
		}
	}
}

type acrPreflightEnvironmentServer struct {
	transactionEnvironmentServer
	binding []byte
	readErr error
}

func (s *acrPreflightEnvironmentServer) GetConfig(
	context.Context, *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return &azdext.GetConfigResponse{Found: s.binding != nil, Value: s.binding}, nil
}

func TestProjectACRBindingPreflight(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	const projectID = "/subscriptions/sub/resourceGroups/project/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	binding, err := json.Marshal(synthesis.ACRBinding{
		Version: 1, Mode: "reuse-connect", RegistryResourceID: testExternalRegistryID,
		ProjectResourceID: projectID, PrincipalID: "principal",
	})
	require.NoError(t, err)
	for _, tt := range []struct {
		name     string
		selector string
		binding  []byte
		readErr  error
		want     string
		envID    string
	}{
		{"same registry", strings.ToUpper(testExternalRegistryID), binding, nil, "", ""},
		{"same registry repairs selector", testExternalRegistryID, binding, nil, "", "manual-change"},
		{"explicit empty selector", "", binding, nil, "full ARM ID", ""},
		{"different registry", testExternalRegistryID + "other", binding, nil, "migration", ""},
		{"invalid binding", testExternalRegistryID, []byte(`{"version":3}`), nil, "binding", ""},
		{"unreadable binding", testExternalRegistryID, nil, errors.New("config unavailable"), "config unavailable", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := &acrPreflightEnvironmentServer{
				transactionEnvironmentServer: transactionEnvironmentServer{values: map[string]string{
					"AZURE_AI_PROJECT_ID": projectID, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testExternalRegistryID,
					"AZD_FOUNDRY_ACR_MODE": "reuse-connect",
				}},
				binding: tt.binding, readErr: tt.readErr,
			}
			if tt.envID != "" {
				server.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = tt.envID
			}
			action := &ProjectAddAction{
				client: newProjectEnvironmentClient(t, server),
				flags:  &projectAddFlags{newProject: true, acrResourceIDSet: true, acrResourceID: tt.selector},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			err := action.preflightProjectACR(t.Context(), t.TempDir())
			if tt.want != "" {
				require.ErrorContains(t, err, tt.want)
			} else {
				require.NoError(t, err)
				require.NotNil(t, action.flags.deployedBinding)
				project := &resolvedProject{PreserveDeployed: true, ACRResourceID: tt.selector}
				assert.False(t, projectIdentityChanged(server.values, "", project))
				plan := planProjectEnvironment(server.values, projectModeNew, project, false)
				effective := applyProjectEnvironmentPlan(server.values, plan)
				assert.Equal(t, projectID, effective["AZURE_AI_PROJECT_ID"])
				assert.Equal(t, "reuse-connect", effective["AZD_FOUNDRY_ACR_MODE"])
				assert.Empty(t, plan.Unsets)
			}
			assert.Zero(t, server.calls)
		})
	}
}

func TestProjectACRLegacyOutputsPreserveIdentity(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	const projectID = "/subscriptions/sub/resourceGroups/project/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	for _, tt := range []struct {
		name, mode, selector string
		wantError            bool
	}{
		{"stable reuse", "reuse-connect", testExternalRegistryID, false},
		{"changed reuse", "reuse-connect", testExternalRegistryID + "other", true},
		{"matching create", "create", testExternalRegistryID, true},
		{"changed create", "create", testExternalRegistryID + "other", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := &acrPreflightEnvironmentServer{
				transactionEnvironmentServer: transactionEnvironmentServer{values: map[string]string{
					"AZURE_AI_PROJECT_ID": projectID, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testExternalRegistryID,
					"AZURE_CONTAINER_REGISTRY_ENDPOINT": "shared.azurecr.io", "AZD_FOUNDRY_ACR_MODE": tt.mode,
					"AZD_FOUNDRY_ACR_PULL_ASSIGNED": "true", "USE_EXISTING_AI_PROJECT": "false",
				}},
			}
			before := maps.Clone(server.values)
			action := &ProjectAddAction{
				client: newProjectEnvironmentClient(t, server),
				flags:  &projectAddFlags{newProject: true, acrResourceIDSet: true, acrResourceID: tt.selector},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			err := action.preflightProjectACR(t.Context(), t.TempDir())
			if tt.wantError {
				require.ErrorContains(t, err, "provisioned Foundry project")
				assert.Empty(t, action.flags.preserveProjectID)
			} else {
				require.NoError(t, err)
				assert.Equal(t, projectID, action.flags.preserveProjectID)
				project := &resolvedProject{PreserveDeployed: true, ACRResourceID: tt.selector}
				plan := planProjectEnvironment(server.values, projectModeNew, project, false)
				assert.Equal(t, before, applyProjectEnvironmentPlan(server.values, plan))
			}
			assert.Nil(t, action.flags.deployedBinding)
			assert.Equal(t, before, server.values)
			assert.Zero(t, server.calls)
		})
	}
}

func TestProjectRegistryOutput(t *testing.T) {
	registry := &projectRegistryOutput{ResourceID: testExternalRegistryID, ExternallyOwned: true}
	data, err := json.Marshal(projectAddOutput{ContainerRegistry: registry})
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, map[string]any{
		"resourceId": testExternalRegistryID, "externallyOwned": true,
	}, result["containerRegistry"])
	var text bytes.Buffer
	writeProjectRegistryOutput(&text, registry)
	assert.Contains(t, text.String(), testExternalRegistryID)
	assert.Contains(t, text.String(), "externally owned")
	text.Reset()
	writeProjectRegistryOutput(&text, nil)
	assert.Empty(t, text.String())
	data, err = json.Marshal(projectAddOutput{})
	require.NoError(t, err)
	assert.NotContains(t, string(data), "containerRegistry")
	for _, format := range []string{"default", "json", "none"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			require.NoError(t, writeProjectAddOutput(&output, format, projectAddOutput{
				ServiceName: "project", Mutation: "unchanged", ContainerRegistry: registry,
			}))
			switch format {
			case "none":
				assert.Empty(t, output.String())
			case "json":
				var parsed map[string]any
				require.NoError(t, json.Unmarshal(output.Bytes(), &parsed))
				assert.Contains(t, parsed, "containerRegistry")
				assert.NotContains(t, output.String(), "Foundry project configuration")
			default:
				assert.Contains(t, output.String(), "externally owned")
				assert.Contains(t, output.String(), testExternalRegistryID)
			}
		})
	}
}

func TestProjectAddACRManifestValidationBeforeMutation(t *testing.T) {
	for _, body := range []string{
		"services:\n  agent:\n    host: azure.ai.agent\n    image: registry/image:v1\n" +
			"    docker: {imagePassthrough: true}\n",
		"services:\n  agent:\n    host: azure.ai.agent\n    codeConfiguration:\n      runtime: python\n",
		"infra:\n  provider: terraform\nservices: {}\n",
	} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			path := filepath.Join(root, "azure.yaml")
			require.NoError(t, os.WriteFile(path, []byte(body), 0600))
			old := map[string]string{"AZD_FOUNDRY_ACR_MODE": "create"}
			server := &acrPreflightEnvironmentServer{
				transactionEnvironmentServer: transactionEnvironmentServer{values: maps.Clone(old)},
			}
			action := &ProjectAddAction{
				client: newProjectEnvironmentClient(t, server),
				flags: &projectAddFlags{
					newProject: true, acrResourceIDSet: true, acrResourceID: testExternalRegistryID,
				},
				extCtx: &azdext.ExtensionContext{Environment: "test"},
			}
			err := action.Run(t.Context())
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "Unimplemented")
			assert.Zero(t, server.calls)
			assert.Equal(t, old, server.values)
			// #nosec G304 -- path is within the test project.
			persisted, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, body, string(persisted))
			assert.NoDirExists(t, filepath.Join(root, ".azure"))
		})
	}
}

func TestProjectAddEnvironmentACRManifestValidation(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	const source = "  agent:\n    host: azure.ai.agent\n    kind: hosted\n"
	const prebuilt = "  agent:\n    host: azure.ai.agent\n    image: registry/image:v1\n" +
		"    docker: {imagePassthrough: true}\n"
	const projectID = "/subscriptions/subscription/resourceGroups/project/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	for _, tt := range []struct {
		name, provider, agent, want string
		binding, missingManifest    bool
	}{
		{name: "prebuilt", agent: prebuilt, want: "prebuilt-image"},
		{name: "code", agent: "  agent:\n    host: azure.ai.agent\n    codeConfiguration:\n      runtime: python\n",
			want: "code"},
		{name: "Terraform source", provider: "terraform", agent: source, want: "Foundry-only infrastructure"},
		{name: "Bicep source", agent: source},
		{name: "Bicep pending project"},
		{name: "canonical prebuilt", agent: prebuilt, binding: true, want: "prebuilt-image"},
		{name: "canonical Bicep source", agent: source, binding: true},
		{name: "canonical pending project", binding: true},
		{name: "missing manifest", missingManifest: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, run := range []bool{false, true} {
				t.Run(map[bool]string{false: "preflight", true: "Run"}[run], func(t *testing.T) {
					root := t.TempDir()
					t.Setenv("AZD_EXEC_PROJECT_DIR", root)
					provider := tt.provider
					if provider == "" {
						provider = "microsoft.foundry"
					}
					raw := []byte("name: test\ninfra:\n  provider: " + provider +
						"\nservices:\n  project:\n    host: azure.ai.project\n" + tt.agent)
					path := filepath.Join(root, "azure.yaml")
					client, project, env, ai, workflow := newSelfInitializingDeploymentClient(t, root)
					if !tt.missingManifest {
						require.NoError(t, os.WriteFile(path, raw, 0o600))
						project.project.Infra = &azdext.InfraOptions{Provider: provider}
						project.project.Services["project"] = &azdext.ServiceConfig{Name: "project", Host: aiProjectHost}
						project.services["project"] = map[string]any{"host": aiProjectHost}
						if tt.agent != "" {
							project.project.Services["agent"] = &azdext.ServiceConfig{Name: "agent", Host: "azure.ai.agent"}
							project.services["agent"] = map[string]any{"host": "azure.ai.agent"}
						}
					}
					env.values["AZURE_AI_ACCOUNT_NAME"] = "configured-account"
					env.values["AZURE_AI_PROJECT_NAME"] = "configured-project"
					if tt.binding {
						binding, err := json.Marshal(synthesis.ACRBinding{
							Version: 1, Mode: "reuse-connect", RegistryResourceID: testExternalRegistryID,
							ProjectResourceID: projectID, PrincipalID: "principal",
						})
						require.NoError(t, err)
						env.config = map[string][]byte{synthesis.ACRBindingConfigKey: binding}
						env.values["AZURE_RESOURCE_GROUP"] = "project"
						env.values["AZURE_AI_ACCOUNT_NAME"] = "account"
						env.values["AZURE_AI_PROJECT_NAME"] = "project"
					} else {
						env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testExternalRegistryID
					}
					if tt.want != "" {
						delete(env.values, "AZURE_TENANT_ID")
					}
					beforeValues := maps.Clone(env.values)
					beforeConfig := maps.Clone(env.config)
					beforeCurrent := env.current
					beforeProject := proto.Clone(project.project)
					beforeServices := make(map[string]map[string]any, len(project.services))
					for name, body := range project.services {
						beforeServices[name] = maps.Clone(body)
					}
					action := &ProjectAddAction{
						client: client,
						flags:  &projectAddFlags{newProject: true, noPrompt: true, output: "none"},
						extCtx: &azdext.ExtensionContext{Environment: "test"},
					}
					var err error
					if run {
						err = action.Run(t.Context())
					} else {
						err = action.preflightProjectACR(t.Context(), root)
					}
					if tt.want != "" {
						require.ErrorContains(t, err, tt.want)
						assert.NotContains(t, err.Error(), "Unimplemented")
					} else {
						require.NoError(t, err)
						if run {
							assert.Equal(t, testExternalRegistryID, env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
							assert.Equal(t, "reuse-connect", env.values["AZD_FOUNDRY_ACR_MODE"])
						}
						if tt.binding {
							assert.Equal(t, projectID, action.flags.preserveProjectID)
						}
					}
					if tt.want != "" || !run {
						assert.Equal(t, beforeValues, env.values)
						assert.Equal(t, beforeCurrent, env.current)
						assert.True(t, proto.Equal(beforeProject, project.project))
						assert.Equal(t, beforeServices, project.services)
						assert.Empty(t, env.setLog)
						assert.False(t, project.providerSet)
						assert.False(t, project.deploymentSet)
						assert.Empty(t, workflow.args)
						assert.Zero(t, ai.calls)
						assert.NoDirExists(t, filepath.Join(root, ".azure"))
					}
					assert.Equal(t, beforeConfig, env.config)
					if !tt.missingManifest {
						// #nosec G304 -- test-owned path.
						persisted, readErr := os.ReadFile(path)
						require.NoError(t, readErr)
						assert.Equal(t, raw, persisted)
					} else if !run {
						assert.NoFileExists(t, path)
					}
				})
			}
		})
	}
}
