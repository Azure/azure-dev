// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.projects/synthesis"
	"azureaiagent/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const testInitACRID = "/subscriptions/sub/resourceGroups/registry-rg/providers/" +
	"Microsoft.ContainerRegistry/registries/shared"

func TestInitACRTemplateUsesTargetScope(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, route := range []string{"azure.yaml", "repository", "interactive repository"} {
		for _, conflict := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/conflict=%t", route, conflict), func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				require.NoError(t, os.WriteFile("azure.yaml", []byte("name: caller\n"), 0o600))
				staging := t.TempDir()
				content := []byte("name: child\ninfra:\n  layers:\n    - name: foundry\n" +
					"      provider: microsoft.foundry\nservices:\n  foundry:\n    host: azure.ai.project\n" +
					"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n")
				require.NoError(t, os.WriteFile(filepath.Join(staging, "azure.yaml"), content, 0o600))
				server := &initACREnvironmentServer{testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_SUBSCRIPTION_ID": "other", "AZURE_FOUNDRY_RESOURCE_GROUP": "rg-old-foundry",
					}},
				}}
				group := "rg-old-foundry"
				if conflict {
					group = "rg-child-dev-foundry"
				}
				flags := &initFlags{
					templatePointer: filepath.Join(staging, "azure.yaml"), src: "child", noPrompt: true,
					acrResourceIDSet: true,
					acrResourceID:    strings.Replace(testInitACRID, "registry-rg", group, 1),
				}
				if route == "interactive repository" {
					flags.templatePointer = ""
				}
				workflow := &recordingProjectWorkflowServer{err: errors.New("stopped at core init")}
				flags.projectsAddHelp = testProjectsACRHelp(workflow)
				client := newTestAzdClient(t, server, workflow)
				var err error
				if route == "azure.yaml" {
					err = runInitFromAzureYaml(t.Context(), flags, client, http.DefaultClient, content, staging, io.Discard)
				} else {
					_, err = scaffoldAzdTemplateProject(
						t.Context(), client, http.DefaultClient, "child", staging, "child-dev", flags, io.Discard,
					)
				}
				if conflict {
					require.ErrorContains(t, err, "same resource-group name")
					require.Empty(t, workflow.requests)
				} else {
					require.ErrorContains(t, err, "stopped at core init")
					require.Len(t, workflow.requests, 1)
					require.Contains(t, workflow.requests[0].Workflow.Steps[0].Command.Args, "child-dev")
				}
				require.Equal(t, "child-dev", flags.resolvedEnvironmentName)
				require.Empty(t, server.setKeys)
				require.NoDirExists(t, filepath.Join(root, "child"))
			})
		}
	}
}

func TestInitACRTemplateReadsOnlyTargetState(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile("azure.yaml", []byte("name: caller\n"), 0o600))
	dir := filepath.Join(root, "child", ".azure", "child-dev")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	state := []byte("AZURE_SUBSCRIPTION_ID=sub\nAZURE_CONTAINER_REGISTRY_RESOURCE_ID=" + testInitACRID + "\n")
	envPath := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envPath, state, 0o600))
	content := []byte("name: child\nservices:\n  foundry:\n    host: azure.ai.project\n" +
		"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n")
	flags := &initFlags{templatePointer: "template.yaml", noPrompt: true}
	require.NoError(t, validateInitACRProjectContent(t.Context(), nil, flags, content, root, "child"))
	require.Equal(t, testInitACRID, flags.effectiveACRResourceID)
	scope, err := planInitACRSubscription(t.Context(), nil, flags, "child")
	require.NoError(t, err)
	require.Equal(t, "sub", scope.SubscriptionId)
	flags.deployMode = "code"
	err = validateInitACRProjectContent(t.Context(), nil, flags, content, root, "child")
	require.ErrorContains(t, err, "--deploy-mode code")
	actual, err := os.ReadFile(envPath)
	require.NoError(t, err)
	require.Equal(t, state, actual)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NoFileExists(t, filepath.Join(root, "child", "azure.yaml"))
}

func TestInitACRTemplateRejectsSavedSelectorInputsBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, route := range []string{"azure.yaml", "repository"} {
		for _, selection := range []string{"absent", "environment", "binding"} {
			for _, tt := range []struct {
				flag, value string
			}{
				{"", ""},
				{"image", "registry.example.com/agent:v1"},
				{"image", ""},
				{"acr-connection", "private-registry"},
				{"acr-connection", ""},
				{"registry-connection", "private-registry"},
				{"registry-connection", ""},
				{"runtime", "python_3_13"},
				{"runtime", ""},
				{"entry-point", "main.py"},
				{"entry-point", ""},
				{"dep-resolution", "remote_build"},
				{"dep-resolution", ""},
				{"project-id", ""},
			} {
				if selection == "absent" && tt.flag != "" && tt.flag != "image" {
					continue
				}
				t.Run(fmt.Sprintf("%s/%s/%s=%q", route, selection, tt.flag, tt.value), func(t *testing.T) {
					root := t.TempDir()
					t.Chdir(root)
					callerContent := []byte("name: caller\n")
					require.NoError(t, os.WriteFile("azure.yaml", callerContent, 0o600))
					dir := filepath.Join(root, "child", ".azure", "child-dev")
					require.NoError(t, os.MkdirAll(dir, 0o750))
					statePath := filepath.Join(dir, ".env")
					state := []byte("AZURE_SUBSCRIPTION_ID=sub\n")
					if selection == "environment" {
						state = append(state, []byte("AZURE_CONTAINER_REGISTRY_RESOURCE_ID="+testInitACRID+"\n")...)
					}
					if selection == "binding" {
						var err error
						state, err = json.Marshal(map[string]any{
							"extensions": map[string]any{"ai-projects": map[string]any{
								"acrBinding": synthesis.ACRBinding{
									Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
									ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
										"Microsoft.CognitiveServices/accounts/account/projects/project",
									PrincipalID: "principal",
								},
							}},
						})
						require.NoError(t, err)
						statePath = filepath.Join(dir, "config.json")
					}
					require.NoError(t, os.WriteFile(statePath, state, 0o600))
					staging := t.TempDir()
					content := []byte("name: child\nservices:\n  foundry:\n    host: azure.ai.project\n" +
						"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n")
					require.NoError(t, os.WriteFile(filepath.Join(staging, "azure.yaml"), content, 0o600))
					flags := &initFlags{src: "child", env: "child-dev", noPrompt: true}
					if route == "azure.yaml" {
						flags.templatePointer = filepath.Join(staging, "azure.yaml")
					}
					cmd := &cobra.Command{}
					for name, target := range map[string]*string{
						"project-id":          &flags.projectResourceId,
						"acr-connection":      &flags.acrConnection,
						"image":               &flags.image,
						"registry-connection": &flags.registryConnection,
						"runtime":             &flags.runtime,
						"entry-point":         &flags.entryPoint,
						"dep-resolution":      &flags.depResolution,
					} {
						cmd.Flags().StringVar(target, name, "", "")
					}
					if tt.flag != "" {
						require.NoError(t, cmd.Flags().Set(tt.flag, tt.value))
					}
					server := &initACREnvironmentServer{}
					workflow := &recordingProjectWorkflowServer{err: errors.New("stopped at core init")}
					flags.projectsAddHelp = testProjectsACRHelp(workflow)
					client := newTestAzdClient(t, server, workflow)
					require.NoError(t, validateInitACRInput(cmd, flags))
					require.NoError(t, validateInitACRProject(t.Context(), client, flags))
					require.Empty(t, flags.effectiveACRResourceID)
					require.NoError(t, validateInitACRInput(cmd, flags))
					var err error
					if route == "azure.yaml" {
						err = runInitFromAzureYaml(t.Context(), flags, client, http.DefaultClient, content, staging, io.Discard)
					} else {
						_, err = scaffoldAzdTemplateProject(
							t.Context(), client, http.DefaultClient, "child", staging, "child-dev", flags, io.Discard,
						)
					}
					if selection != "absent" && tt.flag != "" {
						selector := "AZURE_CONTAINER_REGISTRY_RESOURCE_ID"
						if selection == "binding" {
							selector = "saved ACR binding"
						}
						require.ErrorContains(t, err, selector+" cannot be combined with --"+tt.flag)
						require.Empty(t, workflow.requests)
					} else {
						require.ErrorContains(t, err, "stopped at core init")
						require.Len(t, workflow.requests, 1)
					}
					require.Empty(t, server.setKeys)
					actual, err := os.ReadFile(statePath)
					require.NoError(t, err)
					require.Equal(t, state, actual)
					actual, err = os.ReadFile(filepath.Join(root, "azure.yaml"))
					require.NoError(t, err)
					require.Equal(t, callerContent, actual)
					entries, err := os.ReadDir(filepath.Join(root, "child"))
					require.NoError(t, err)
					require.Len(t, entries, 1)
					entries, err = os.ReadDir(dir)
					require.NoError(t, err)
					require.Len(t, entries, 1)
					require.NoDirExists(t, filepath.Join(root, ".azure"))
					require.NoFileExists(t, filepath.Join(root, "child", "azure.yaml"))
				})
			}
		}
	}
}

func TestInitACRTargetBindingAndReadErrors(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			dir := filepath.Join(root, "child", ".azure", "child-dev")
			require.NoError(t, os.MkdirAll(dir, 0o750))
			binding := synthesis.ACRBinding{
				Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
				ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
					"Microsoft.CognitiveServices/accounts/account/projects/project",
				PrincipalID: "principal",
			}
			data, err := json.Marshal(map[string]any{
				"extensions": map[string]any{"ai-projects": map[string]any{"acrBinding": binding}},
			})
			require.NoError(t, err)
			if corrupt {
				data = []byte("{")
			}
			path := filepath.Join(dir, "config.json")
			require.NoError(t, os.WriteFile(path, data, 0o600))
			flags := &initFlags{templatePointer: "template.yaml", noPrompt: true}
			content := []byte("name: child\nservices:\n  foundry:\n    host: azure.ai.project\n")
			err = validateInitACRProjectContent(t.Context(), nil, flags, content, root, "child")
			if corrupt {
				require.ErrorContains(t, err, "read target registry binding")
			} else {
				require.NoError(t, err)
				require.Equal(t, testInitACRID, flags.effectiveACRResourceID)
			}
			actual, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, data, actual)
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, entries, 1)
		})
	}
}

func TestInitACRSavedSourceSubscriptionRejectsBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, canonical := range []bool{false, true} {
		t.Run(fmt.Sprint(canonical), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n")
			require.NoError(t, os.WriteFile("azure.yaml", content, 0o600))
			require.NoError(t, os.WriteFile("main.py", []byte("print('hello')\n"), 0o600))
			values := map[string]string{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID}
			server := &reuseInitACREnvironmentServer{testEnvironmentServiceServer: testEnvironmentServiceServer{
				environments: map[string]*azdext.Environment{"test": {Name: "test"}},
				current:      &azdext.Environment{Name: "test"}, values: map[string]map[string]string{"test": values},
				config: map[string][]byte{},
			}}
			if canonical {
				binding, err := json.Marshal(synthesis.ACRBinding{
					Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
					ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
						"Microsoft.CognitiveServices/accounts/account/projects/project",
					PrincipalID: "principal",
				})
				require.NoError(t, err)
				server.config[synthesis.ACRBindingConfigKey] = binding
				delete(values, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
			}
			before := maps.Clone(values)
			prompts := &sourceInitACRPromptServer{initACRPromptServer: initACRPromptServer{
				subscription: &azdext.Subscription{Id: "other", UserTenantId: "user-tenant"},
			}}
			workflow := &recordingProjectWorkflowServer{}
			projects := &recordingProjectServer{projectPath: root}
			err := executeReuseInitACR(t, server, workflow, prompts, projects, &sourceInitACRAccountServer{},
				false, "test", "--src", ".", "--agent-name", "agent", "--protocol", "responses")
			require.ErrorContains(t, err, "same subscription")
			require.Equal(t, 1, prompts.calls)
			require.Zero(t, prompts.locationCalls)
			require.Equal(t, before, values)
			require.Empty(t, server.setKeys)
			require.Empty(t, workflow.requests)
			require.Empty(t, projects.added)
			require.NoFileExists(t, ".agentignore")
			actual, err := os.ReadFile("azure.yaml")
			require.NoError(t, err)
			require.Equal(t, content, actual)
		})
	}
}

func TestResolveACREnvironmentUsesActualAgentName(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile("azure.yaml", []byte("name: test\n"), 0o600))
	server := &reuseInitACREnvironmentServer{}
	client := newTestAzdClient(t, server, &testWorkflowServiceServer{})
	flags := &initFlags{
		acrResourceIDSet: true,
		agentName:        "chat",
	}
	action := &InitFromCodeAction{azdClient: client, flags: flags}

	require.NoError(t, action.resolveACREnvironment(t.Context()))
	require.Equal(t, "chat", flags.resolvedAgentName)
	require.Equal(t, "chat-dev", flags.resolvedEnvironmentName)

	envName, values, err := readInitACREnvironment(t.Context(), client, flags, ".")
	require.NoError(t, err)
	require.Empty(t, envName)
	require.Equal(t, "chat-dev", values["AZURE_ENV_NAME"])
}

func TestInitACRPlannedEnvironmentDoesNotReadBinding(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, tt := range []struct {
		name, explicit, planned, wantName string
		existingProject                   bool
	}{
		{name: "fresh project", wantName: "demo-dev"},
		{name: "planned environment", existingProject: true, planned: "chat-dev", wantName: "chat-dev"},
		{name: "explicit environment", existingProject: true, explicit: "explicit", wantName: "explicit"},
		{name: "explicit overrides plan", existingProject: true,
			explicit: "explicit", planned: "chat-dev", wantName: "explicit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "demo")
			require.NoError(t, os.Mkdir(root, 0o700))
			t.Chdir(root)
			if tt.existingProject {
				require.NoError(t, os.WriteFile("azure.yaml", []byte("name: test\n"), 0o600))
			}
			server := &reuseInitACREnvironmentServer{testEnvironmentServiceServer: testEnvironmentServiceServer{
				configErr: errors.New("unexpected binding read for a missing environment"),
			}}
			client := newTestAzdClient(t, server, &testWorkflowServiceServer{})
			flags := &initFlags{
				env: tt.explicit, resolvedEnvironmentName: tt.planned,
				acrResourceIDSet: true, acrResourceID: testInitACRID, noPrompt: true,
			}
			envName, values, err := readInitACREnvironment(t.Context(), client, flags, ".")
			require.NoError(t, err)
			require.Empty(t, envName)
			require.Equal(t, tt.wantName, values["AZURE_ENV_NAME"])
			_, err = planInitACRSubscription(t.Context(), client, flags, ".")
			require.NoError(t, err)
			require.Zero(t, server.getValuesCalls)
			require.Empty(t, server.setKeys)
		})
	}
}

func TestInitACRSelectionSpecificGuidance(t *testing.T) {
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
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\n" + tt.manifest)
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0o600))
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {"AZURE_SUBSCRIPTION_ID": "sub"}},
				},
			}
			client := newTestAzdClient(t, server, &recordingProjectWorkflowServer{})
			err := validateInitACRProject(t.Context(), client, &initFlags{
				acrResourceIDSet: true, acrResourceID: testInitACRID,
			})
			require.ErrorContains(t, err, tt.want)
			require.Empty(t, azdext.ErrorSuggestion(err))
			require.Empty(t, server.setKeys)
		})
	}
}

func TestInitACRRejectsInvalidInputsBeforeSideEffects(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("AZD_SERVER", "127.0.0.1:1")
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"empty", []string{"--acr-resource-id="}, "non-empty ACR ARM resource ID"},
		{"whitespace", []string{"--acr-resource-id", " "}, "non-empty ACR ARM resource ID"},
		{"malformed", []string{"--acr-resource-id", "registry.azurecr.io"}, "ACR ARM resource ID"},
		{"wrong resource", []string{"--acr-resource-id", "/subscriptions/sub/resourceGroups/rg/" +
			"providers/Microsoft.Storage/storageAccounts/storage"}, "ACR ARM resource ID"},
		{"image", []string{"--image=image"}, "--image"},
		{"empty image", []string{"--image="}, "--image"},
		{"connection", []string{"--acr-connection=existing"}, "--acr-connection"},
		{"registry connection", []string{"--registry-connection=private"}, "--registry-connection"},
		{"project", []string{"--project-id=existing"}, "--project-id"},
		{"code", []string{"--deploy-mode=code"}, "--deploy-mode code"},
		{"prompt", []string{"--kind=prompt"}, "--kind prompt"},
		{"voice", []string{"--kind=prompt-voice"}, "--kind prompt-voice"},
		{"terraform", []string{"--infra=terraform"}, "--infra terraform"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.args
			if tt.name != "empty" && tt.name != "whitespace" &&
				tt.name != "malformed" && tt.name != "wrong resource" {
				args = append([]string{"--acr-resource-id", testInitACRID}, args...)
			}
			cmd := newInitCommand(&azdext.ExtensionContext{NoPrompt: true})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(args)
			require.ErrorContains(t, cmd.Execute(), tt.want)
			files, err := os.ReadDir(root)
			require.NoError(t, err)
			require.Empty(t, files)
		})
	}
}

func TestAuthorFoundryProjectForwardsACRSelector(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, mode := range []projectAuthoringMode{projectAuthoringNew, projectAuthoringCurrent} {
		t.Run(string(mode), func(t *testing.T) {
			workflow := &recordingProjectWorkflowServer{}
			client := newProjectWorkflowClient(t, &recordingProjectServer{
				existing: map[string]*azdext.ServiceConfig{},
			}, workflow)
			root := t.TempDir()
			require.NoError(t, authorFoundryProject(
				t.Context(), client, nil, root, mode, true, testInitACRID,
			))
			require.Equal(t, expectedProjectWorkflowArgs(t, root,
				"ai", "project", "add", "--no-prompt", "--output", "none",
				"--new-project", "--acr-resource-id", testInitACRID,
			), workflowArgs(t, workflow, 0))
		})
	}
}

func TestAuthorFoundryProjectRejectsExistingProjectACRSelector(t *testing.T) {
	t.Parallel()
	err := authorFoundryProject(t.Context(), nil, &FoundryProjectInfo{
		ResourceId: "existing-project",
	}, ".", projectAuthoringExisting, true, testInitACRID)
	require.ErrorContains(t, err, "--acr-resource-id cannot be combined with --project-id")
}

func TestInitACRManifestSourceBuildValidation(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, tt := range []struct {
		name    string
		service string
		wantErr bool
	}{
		{"source", "kind: hosted", false},
		{"image defaults to source", "kind: hosted\n    image: registry.example.com/agent:v1", false},
		{"prebuilt", "kind: hosted\n    image: registry.example.com/agent:v1\n" +
			"    docker: {imagePassthrough: true}", true},
		{"prompt", "kind: prompt", true},
		{"voice", "kind: prompt-voice", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n" +
				"  agent:\n    host: azure.ai.agent\n    " + tt.service + "\n")
			err := validateInitACRManifest(t.Context(), nil, content, root, &initFlags{
				acrResourceIDSet: true, acrResourceID: testInitACRID,
			}, ".")
			if tt.wantErr {
				require.ErrorContains(t, err, "without hosted-container source builds")
			} else {
				require.NoError(t, err)
			}
			entries, err := os.ReadDir(root)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestInitACRManifestResolvesRefs(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"),
		[]byte("kind: prompt\n"), 0o600))
	err := validateInitACRManifest(t.Context(), nil, []byte("name: test\nservices:\n  foundry:\n"+
		"    host: azure.ai.project\n  agent:\n    host: azure.ai.agent\n    $ref: agent.yaml\n"),
		root, &initFlags{acrResourceIDSet: true, acrResourceID: testInitACRID}, ".")
	require.ErrorContains(t, err, "without hosted-container source builds")
}

func TestInitACRManifestValidatesPlannedEnvironmentBeforeScaffolding(t *testing.T) {
	for _, route := range []string{"manifest", "azure.yaml", "repository"} {
		for _, tt := range []struct {
			name, group, subscription, wantError   string
			layer, existing, defaultEnv, noCurrent bool
		}{
			{name: "planned root group", group: "rg-demo", wantError: "same resource-group name"},
			{name: "planned layer group", layer: true, group: "rg-demo-foundry", wantError: "same resource-group name"},
			{name: "derived root group", defaultEnv: true, group: "rg-project-dev",
				wantError: "same resource-group name"},
			{name: "derived layer group", defaultEnv: true, layer: true, group: "rg-project-dev-foundry",
				wantError: "same resource-group name"},
			{name: "derived group without current environment", existing: true, defaultEnv: true, noCurrent: true,
				group: "rg-project-dev", wantError: "same resource-group name"},
			{name: "existing root environment", existing: true, group: "rg-demo", subscription: "sub",
				wantError: "same resource-group name"},
			{name: "current environment without explicit selector", existing: true, defaultEnv: true,
				group: "rg-demo", subscription: "sub"},
			{name: "existing layer environment", existing: true, layer: true,
				group: "rg-demo-foundry", subscription: "sub", wantError: "same resource-group name"},
			{name: "configured subscription mismatch", existing: true, group: "registry-rg",
				subscription: "other"},
			{name: "external registry", group: "registry-rg"},
			{name: "matching subscription", existing: true, group: "registry-rg", subscription: "sub"},
		} {
			t.Run(route+"/"+tt.name, func(t *testing.T) {
				t.Setenv(synthesis.ACRReuseEnvVar, "true")
				root := t.TempDir()
				t.Chdir(root)
				templateDir := t.TempDir()
				content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n" +
					"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n")
				if tt.layer {
					content = append(content, []byte(
						"infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n")...)
				}
				require.NoError(t, os.WriteFile(filepath.Join(templateDir, "azure.yaml"), content, 0o600))
				environments := &reuseInitACREnvironmentServer{testEnvironmentServiceServer: testEnvironmentServiceServer{
					environments: map[string]*azdext.Environment{},
					values:       map[string]map[string]string{},
				}}
				existingContent := []byte("name: existing\n")
				if tt.existing {
					require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), existingContent, 0o600))
					environments.environments["demo"] = &azdext.Environment{Name: "demo"}
					environments.current = environments.environments["demo"]
					environments.values["demo"] = map[string]string{"AZURE_SUBSCRIPTION_ID": tt.subscription}
					if tt.noCurrent {
						environments.current = nil
					}
				}
				before := maps.Clone(environments.values["demo"])
				workflow := &recordingProjectWorkflowServer{err: errors.New("stopped at core init")}
				prompts := &initACRPromptServer{}
				client := newTestAzdClient(t, environments, workflow, prompts)
				flags := &initFlags{
					acrResourceIDSet: true,
					acrResourceID:    strings.Replace(testInitACRID, "/registry-rg/", "/"+tt.group+"/", 1),
					env:              "demo", src: "project", noPrompt: true,
					templatePointer: filepath.Join(templateDir, "azure.yaml"),
					projectsAddHelp: testProjectsACRHelp(workflow),
				}
				if tt.defaultEnv {
					flags.env = ""
					if route == "repository" {
						flags.src = ""
					}
				}
				var err error
				switch route {
				case "manifest":
					err = validateInitACRManifest(t.Context(), client, content, templateDir, flags, "project")
				case "azure.yaml":
					err = runInitFromAzureYaml(t.Context(), flags, client, http.DefaultClient, content, templateDir, io.Discard)
				case "repository":
					_, err = scaffoldAzdTemplateProject(
						t.Context(), client, http.DefaultClient, "project", templateDir, "demo", flags, io.Discard,
					)
				}
				if tt.wantError != "" {
					require.ErrorContains(t, err, tt.wantError)
					require.Empty(t, workflow.requests)
				} else if route == "manifest" {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "stopped at core init")
					require.Len(t, workflow.requests, 1)
				}
				require.Zero(t, prompts.calls)
				require.Empty(t, environments.setKeys)
				require.Equal(t, before, environments.values["demo"])
				require.NoDirExists(t, filepath.Join(root, "project"))
				require.NoDirExists(t, filepath.Join(root, ".azure"))
				entries, err := os.ReadDir(root)
				require.NoError(t, err)
				if tt.existing {
					require.Len(t, entries, 1)
					after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
					require.NoError(t, err)
					require.Equal(t, existingContent, after)
				} else {
					require.Empty(t, entries)
				}
				after, err := os.ReadFile(filepath.Join(templateDir, "azure.yaml"))
				require.NoError(t, err)
				require.Equal(t, content, after)
			})
		}
	}
}

func TestPreflightAzdTemplateRejectsUnsupportedACRBeforeScaffolding(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	root := t.TempDir()
	t.Chdir(root)
	for _, tt := range []struct {
		name       string
		properties string
	}{
		{"prebuilt", "kind: hosted\n    image: registry.example.com/agent:v1\n" +
			"    docker: {imagePassthrough: true}"},
		{"referenced prompt", "$ref: agent.yaml"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			templateDir := t.TempDir()
			content := "name: test\nservices:\n  foundry:\n    host: azure.ai.project\n" +
				"  agent:\n    host: azure.ai.agent\n    name: agent\n    " + tt.properties + "\n"
			require.NoError(t, os.WriteFile(
				filepath.Join(templateDir, "azure.yaml"), []byte(content), 0o600,
			))
			require.NoError(t, os.WriteFile(
				filepath.Join(templateDir, "agent.yaml"), []byte("kind: prompt\n"), 0o600,
			))
			workflow := &testWorkflowServiceServer{}
			client := newTestAzdClient(t, &testEnvironmentServiceServer{}, workflow)
			replaced, err := preflightAzdTemplate(
				t.Context(), client, http.DefaultClient, templateDir,
				&initFlags{acrResourceIDSet: true, acrResourceID: testInitACRID},
				".", io.Discard,
			)
			require.ErrorContains(t, err, "without hosted-container source builds")
			require.Empty(t, replaced)
			require.Zero(t, workflow.runCalls)
			entries, err := os.ReadDir(root)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

type initACRPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	subscription *azdext.Subscription
	err          error
	calls        int
}

func (s *initACRPromptServer) PromptSubscription(
	context.Context, *azdext.PromptSubscriptionRequest,
) (*azdext.PromptSubscriptionResponse, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &azdext.PromptSubscriptionResponse{Subscription: s.subscription}, nil
}

func TestInitACRTemplateRejectsExistingProjectBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, route := range []string{"azure.yaml", "repository"} {
		for _, declaration := range []string{"inline", "reference"} {
			t.Run(route+"/"+declaration, func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				templateDir := t.TempDir()
				projectBody := "host: azure.ai.project\n" +
					"endpoint: https://account.services.ai.azure.com/api/projects/project\n"
				projectEntry := "    " + strings.ReplaceAll(strings.TrimSpace(projectBody), "\n", "\n    ")
				if declaration == "reference" {
					projectEntry = "    $ref: project.yaml"
					require.NoError(t, os.WriteFile(
						filepath.Join(templateDir, "project.yaml"), []byte(projectBody), 0o600,
					))
				}
				content := []byte("name: test\nservices:\n  foundry:\n" + projectEntry +
					"\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n")
				require.NoError(t, os.WriteFile(filepath.Join(templateDir, "azure.yaml"), content, 0o600))
				require.NoError(t, validateInitACRManifest(t.Context(), nil, content, templateDir, &initFlags{}, "."))
				environments := &testEnvironmentServiceServer{}
				workflow := &testWorkflowServiceServer{}
				prompts := &initACRPromptServer{}
				client := newTestAzdClient(t, environments, workflow, prompts)
				flags := &initFlags{
					acrResourceIDSet: true, acrResourceID: testInitACRID,
					src: "project", templatePointer: filepath.Join(templateDir, "azure.yaml"),
				}

				var err error
				if route == "azure.yaml" {
					err = runInitFromAzureYaml(t.Context(), flags, client, http.DefaultClient, content, templateDir, io.Discard)
				} else {
					_, err = scaffoldAzdTemplateProject(
						t.Context(), client, http.DefaultClient, "project", templateDir, "test", flags, io.Discard,
					)
				}
				require.ErrorContains(t, err, "only supported for a new Foundry project")
				require.Zero(t, workflow.runCalls)
				require.Zero(t, prompts.calls)
				require.Empty(t, environments.setKeys)
				entries, err := os.ReadDir(root)
				require.NoError(t, err)
				require.Empty(t, entries)
				after, err := os.ReadFile(filepath.Join(templateDir, "azure.yaml"))
				require.NoError(t, err)
				require.Equal(t, content, after)
			})
		}
	}
}

func TestInitACRTemplateValidatesSubscriptionBeforeScaffolding(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, route := range []string{"azure.yaml", "repository"} {
		for _, tt := range []struct {
			name, subscription, wantError string
			promptError                   error
			noPrompt                      bool
			wantPrompts, wantWorkflows    int
		}{
			{"different subscription", "other", "same subscription", nil, false, 1, 0},
			{"cancelled", "", "subscription selection was cancelled",
				status.Error(codes.Canceled, "cancelled"), false, 1, 0},
			{"matching subscription", "SUB", "stopped at core init", nil, false, 1, 1},
			{"deferred headless", "", "stopped at core init", nil, true, 0, 1},
		} {
			t.Run(route+"/"+tt.name, func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				templateDir := t.TempDir()
				content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n" +
					"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n")
				require.NoError(t, os.WriteFile(filepath.Join(templateDir, "azure.yaml"), content, 0o600))
				environments := &testEnvironmentServiceServer{}
				workflow := &testWorkflowServiceServer{runErr: errors.New("stopped at core init")}
				prompts := &initACRPromptServer{
					subscription: &azdext.Subscription{
						Id: tt.subscription, TenantId: "resource-tenant", UserTenantId: "user-tenant",
					},
					err: tt.promptError,
				}
				client := newTestAzdClient(t, environments, workflow, prompts)
				flags := &initFlags{
					acrResourceIDSet: true, acrResourceID: testInitACRID, noPrompt: tt.noPrompt,
					src: "project", templatePointer: filepath.Join(templateDir, "azure.yaml"),
					projectsAddHelp: testProjectsACRHelp(&recordingProjectWorkflowServer{}),
				}

				var err error
				if route == "azure.yaml" {
					err = runInitFromAzureYaml(t.Context(), flags, client, http.DefaultClient, content, templateDir, io.Discard)
				} else {
					_, err = scaffoldAzdTemplateProject(
						t.Context(), client, http.DefaultClient, "project", templateDir, "test", flags, io.Discard,
					)
				}
				require.ErrorContains(t, err, tt.wantError)
				require.Equal(t, tt.wantWorkflows, workflow.runCalls)
				require.Equal(t, tt.wantPrompts, prompts.calls)
				require.Empty(t, environments.setKeys)
				entries, err := os.ReadDir(root)
				require.NoError(t, err)
				require.Empty(t, entries)
			})
		}
	}
}

func TestInitACRTemplateRetainsPlannedSubscriptionWithoutWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	root := t.TempDir()
	t.Chdir(root)
	templateDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "azure.yaml"), []byte(
		"name: test\nservices:\n  foundry:\n    host: azure.ai.project\n"+
			"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n"), 0o600))
	environments := &testEnvironmentServiceServer{}
	workflow := &testWorkflowServiceServer{}
	prompts := &initACRPromptServer{subscription: &azdext.Subscription{
		Id: "sub", TenantId: "resource-tenant", UserTenantId: "user-tenant",
	}}
	client := newTestAzdClient(t, environments, workflow, prompts)

	plan, err := preflightAzdTemplate(t.Context(), client, http.DefaultClient, templateDir, &initFlags{
		acrResourceIDSet: true, acrResourceID: testInitACRID,
		projectsAddHelp: testProjectsACRHelp(&recordingProjectWorkflowServer{}),
	}, ".", io.Discard)
	require.NoError(t, err)
	require.NotNil(t, plan.subscription)
	require.Equal(t, "sub", plan.subscription.SubscriptionId)
	require.Equal(t, "user-tenant", plan.subscription.TenantId)
	require.Equal(t, 1, prompts.calls)
	require.Zero(t, workflow.runCalls)
	require.Empty(t, environments.setKeys)
}

func TestFinalizeAdoptedProjectUsesPlannedACRSubscription(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, wantError string
		subscription    *azdext.AzureScope
	}{
		{"unplanned mismatch", "same subscription", nil},
		{"planned subscription wins", "stopped after subscription preflight",
			&azdext.AzureScope{SubscriptionId: "sub", TenantId: "user-tenant"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			environments := &testEnvironmentServiceServer{
				environments: map[string]*azdext.Environment{"test": {Name: "test"}},
				values:       map[string]map[string]string{"test": {"AZURE_SUBSCRIPTION_ID": "other"}},
			}
			address := newInvokeRemoteContextTestAzdServer(t,
				&helpersProjectServer{err: errors.New("stopped after subscription preflight")}, environments,
			)
			client, err := azdext.NewAzdClient(azdext.WithAddress(address))
			require.NoError(t, err)
			t.Cleanup(func() { client.Close() })

			err = finalizeAdoptedProject(t.Context(), &initFlags{
				acrResourceIDSet: true, acrResourceID: testInitACRID,
			}, client, "test", "", false, "", TemplateTypeAzureYaml,
				adoptedProjectPreflight{subscription: tt.subscription})
			require.ErrorContains(t, err, tt.wantError)
			require.Empty(t, environments.setKeys)
		})
	}
}

type initACREnvironmentServer struct {
	testEnvironmentServiceServer
	binding      []byte
	pending      []byte
	bindingReads int
}

func (s *initACREnvironmentServer) List(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentListResponse, error) {
	return &azdext.EnvironmentListResponse{Environments: []*azdext.EnvironmentDescription{
		{Name: "test", Default: true},
	}}, nil
}

func (s *initACREnvironmentServer) GetConfig(
	_ context.Context, request *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	if request.GetPath() == synthesis.ACRPendingConfigKey {
		return &azdext.GetConfigResponse{Found: s.pending != nil, Value: s.pending}, nil
	}
	s.bindingReads++
	return &azdext.GetConfigResponse{Found: s.binding != nil, Value: s.binding}, nil
}

func TestInitACRTemplateIgnoresCallerReuseState(t *testing.T) {
	for _, route := range []string{"azure.yaml", "repository"} {
		for _, tt := range []struct {
			name, group               string
			enabled, binding, pending bool
		}{
			{name: "disabled aliases"},
			{name: "disabled binding", binding: true},
			{name: "disabled pending", pending: true},
			{name: "enabled pending", enabled: true, pending: true},
			{name: "retained group aliases", enabled: true, group: "registry-rg"},
			{name: "retained group binding", enabled: true, binding: true,
				group: "registry-rg"},
			{name: "external registry", enabled: true, group: "foundry-rg"},
		} {
			t.Run(route+"/"+tt.name, func(t *testing.T) {
				t.Setenv(synthesis.ACRReuseEnvVar, fmt.Sprint(tt.enabled))
				root := t.TempDir()
				t.Chdir(root)
				current := []byte("name: current\nservices:\n  foundry:\n    host: azure.ai.project\n" +
					"    endpoint: https://account.services.ai.azure.com/api/projects/project\n")
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), current, 0o600))
				templateDir := t.TempDir()
				content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n" +
					"    $ref: project.yaml\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n")
				require.NoError(t, os.WriteFile(filepath.Join(templateDir, "azure.yaml"), content, 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(templateDir, "project.yaml"), []byte("name: new-project\n"), 0o600))
				values := map[string]string{
					"AZURE_SUBSCRIPTION_ID":                "sub",
					"AZURE_RESOURCE_GROUP":                 tt.group,
					"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
					"AZD_FOUNDRY_ACR_MODE":                 "reuse-connect",
				}
				environments := &initACREnvironmentServer{
					testEnvironmentServiceServer: testEnvironmentServiceServer{
						values: map[string]map[string]string{"test": values},
					},
				}
				if tt.binding {
					binding, err := json.Marshal(synthesis.ACRBinding{
						Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
						ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
							"Microsoft.CognitiveServices/accounts/account/projects/project",
						PrincipalID: "principal",
					})
					require.NoError(t, err)
					environments.binding = binding
					delete(values, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
					delete(values, "AZD_FOUNDRY_ACR_MODE")
				}
				if tt.pending {
					environments.pending = []byte(`{"version":1}`)
				}
				before := maps.Clone(values)
				workflow := &testWorkflowServiceServer{runErr: errors.New("stopped at core init")}
				prompts := &initACRPromptServer{}
				client := newTestAzdClient(t, environments, workflow, prompts)
				flags := &initFlags{
					src: "project", templatePointer: filepath.Join(templateDir, "azure.yaml"),
				}

				var err error
				if route == "azure.yaml" {
					err = runInitFromAzureYaml(
						t.Context(), flags, client, http.DefaultClient, content, templateDir, io.Discard,
					)
				} else {
					_, err = scaffoldAzdTemplateProject(
						t.Context(), client, http.DefaultClient, "project", templateDir, "test", flags, io.Discard,
					)
				}
				require.ErrorContains(t, err, "stopped at core init")
				require.Equal(t, 1, workflow.runCalls)
				require.Empty(t, flags.effectiveACRResourceID)
				require.Zero(t, prompts.calls)
				require.Equal(t, before, values)
				require.Empty(t, environments.setKeys)
				require.NoDirExists(t, filepath.Join(root, "project"))
				require.NoDirExists(t, filepath.Join(root, ".azure"))
				after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
				require.NoError(t, err)
				require.Equal(t, current, after)
				after, err = os.ReadFile(filepath.Join(templateDir, "azure.yaml"))
				require.NoError(t, err)
				require.Equal(t, content, after)
			})
		}
	}
}

func TestInitACRTemplatePendingTargetBeforeWrites(t *testing.T) {
	for _, route := range []string{"azure.yaml", "repository", "interactive repository"} {
		for _, pending := range []string{"{}", "null"} {
			for _, enabled := range []string{"false", "true"} {
				t.Run(route+"/"+pending+"/"+enabled, func(t *testing.T) {
					t.Setenv(synthesis.ACRReuseEnvVar, enabled)
					root := t.TempDir()
					t.Chdir(root)
					require.NoError(t, os.WriteFile("azure.yaml", []byte("name: caller\n"), 0o600))
					target := filepath.Join(root, "child", ".azure", "child-dev")
					require.NoError(t, os.MkdirAll(target, 0o750))
					require.NoError(t, os.WriteFile(filepath.Join(target, ".env"),
						[]byte("AZURE_SUBSCRIPTION_ID=sub\n"), 0o600))
					state := []byte(`{"extensions":{"ai-projects":{"acrPending":` + pending + `}}}`)
					require.NoError(t, os.WriteFile(filepath.Join(target, "config.json"), state, 0o600))
					staging := t.TempDir()
					content := []byte("name: child\nservices:\n  project:\n    host: azure.ai.project\n" +
						"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n")
					require.NoError(t, os.WriteFile(filepath.Join(staging, "azure.yaml"), content, 0o600))
					flags := &initFlags{src: "child", noPrompt: true}
					if route != "interactive repository" {
						flags.templatePointer = filepath.Join(staging, "azure.yaml")
					}
					environments := &initACREnvironmentServer{}
					workflow := &recordingProjectWorkflowServer{err: errors.New("stopped at core init")}
					prompts := &initACRPromptServer{}
					client := newTestAzdClient(t, environments, workflow, prompts)
					before := testutil.Snapshot(t, root)
					var err error
					if route == "azure.yaml" {
						err = runInitFromAzureYaml(
							t.Context(), flags, client, http.DefaultClient, content, staging, io.Discard,
						)
					} else {
						_, err = scaffoldAzdTemplateProject(
							t.Context(), client, http.DefaultClient, "child", staging, "child-dev", flags, io.Discard,
						)
					}
					if enabled == "false" {
						requireACRFeatureDisabled(t, err)
						require.Empty(t, workflow.requests)
					} else {
						require.ErrorContains(t, err, "stopped at core init")
						require.Len(t, workflow.requests, 1)
					}
					require.Zero(t, prompts.calls)
					require.Empty(t, environments.setKeys)
					require.Equal(t, "child-dev", flags.resolvedEnvironmentName)
					require.Equal(t, before, testutil.Snapshot(t, root))
				})
			}
		}
	}
}

func TestInitACRExplicitExistingProjectLegacyMode(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "false")
	for _, state := range []string{"legacy", "canonical", "pending"} {
		t.Run(state, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  project:\n    host: azure.ai.project\n")
			require.NoError(t, os.WriteFile("azure.yaml", content, 0o600))
			environments := acrFeatureEnvironment(t, state)
			environments.values["test"]["AZD_FOUNDRY_ACR_MODE"] = "reuse-connect"
			environments.values["test"]["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testInitACRID
			workflow := &recordingProjectWorkflowServer{}
			prompts := &initACRPromptServer{}
			client := newTestAzdClient(t, environments, workflow, prompts)
			flags := &initFlags{env: "test", projectResourceId: "/subscriptions/sub/resourceGroups/foundry-rg/" +
				"providers/Microsoft.CognitiveServices/accounts/account/projects/project"}
			before := maps.Clone(environments.values["test"])
			err := validateInitACRProject(t.Context(), client, flags)
			if state == "legacy" {
				require.NoError(t, err)
			} else {
				requireACRFeatureDisabled(t, err)
			}
			require.Empty(t, flags.effectiveACRResourceID)
			require.Zero(t, prompts.calls)
			require.Empty(t, workflow.requests)
			require.Empty(t, environments.setKeys)
			require.Equal(t, before, environments.values["test"])
			after, err := os.ReadFile("azure.yaml")
			require.NoError(t, err)
			require.Equal(t, content, after)
		})
	}
}

func TestPlanInitACRSubscriptionUsesConfiguredSubscription(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, subscription := range []string{"SUB", "other"} {
		t.Run(subscription, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0o600))
			environments := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_SUBSCRIPTION_ID": subscription, "AZURE_TENANT_ID": "user-tenant",
					}},
				},
			}
			prompts := &initACRPromptServer{}
			client := newTestAzdClient(t, environments, &testWorkflowServiceServer{}, prompts)
			scope, err := planInitACRSubscription(t.Context(), client, &initFlags{
				acrResourceIDSet: true, acrResourceID: testInitACRID,
			}, ".")
			if subscription == "other" {
				require.ErrorContains(t, err, "same subscription")
				require.Nil(t, scope)
			} else {
				require.NoError(t, err)
				require.Equal(t, subscription, scope.SubscriptionId)
				require.Equal(t, "user-tenant", scope.TenantId)
			}
			require.Zero(t, prompts.calls)
			require.Empty(t, environments.setKeys)
		})
	}
}

func TestInitACRBindingPreflight(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, tt := range []struct {
		name     string
		mode     string
		selected string
		template string
		wantErr  bool
	}{
		{"same registry", "reuse-connect", testInitACRID, "", false},
		{"different registry", "reuse-connect", testInitACRID + "other", "", true},
		{"create to reuse", "create", testInitACRID, "", true},
		{"explicit empty selector", "reuse-connect", "", "", true},
		{"template same registry", "reuse-connect", testInitACRID, "template.yaml", false},
		{"template different registry", "reuse-connect", testInitACRID + "other", "template.yaml", true},
		{"template create to reuse", "create", testInitACRID, "template.yaml", true},
		{"template empty selector", "reuse-connect", "", "template.yaml", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  foundry:\n" +
				"    host: azure.ai.project\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0o600))
			binding, err := json.Marshal(synthesis.ACRBinding{
				Version: 1, Mode: tt.mode, RegistryResourceID: testInitACRID,
				ProjectResourceID: "/subscriptions/sub/resourceGroups/rg/providers/" +
					"Microsoft.CognitiveServices/accounts/account/projects/project",
				PrincipalID: "principal",
			})
			require.NoError(t, err)
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
						"AZD_FOUNDRY_ACR_MODE":                 tt.mode,
					}},
				},
				binding: binding,
			}

			client := newTestAzdClient(t, server, &recordingProjectWorkflowServer{})
			err = validateInitACRProject(t.Context(), client, &initFlags{
				acrResourceIDSet: true, acrResourceID: tt.selected,
				templatePointer: tt.template,
			})
			if tt.wantErr && (tt.template == "" || tt.selected == "") {
				if tt.selected == "" {
					require.ErrorContains(t, err, "--acr-resource-id")
				} else {
					require.ErrorContains(t, err, "migration")
				}
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, server.setKeys)
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, after)
		})
	}
}

func TestInitACRExplicitSelectionChecksDeployedStateBeforeWrites(t *testing.T) {
	const projectID = "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	for _, route := range []string{"preflight", "staged", "subscription", "source", "azure.yaml", "repository"} {
		for _, tt := range []struct {
			name, mode, output, selector string
			canonical, noProjectID       bool
			wantError                    bool
		}{
			{name: "legacy create matching", mode: "create", output: "AZURE_CONTAINER_REGISTRY_ENDPOINT", wantError: true},
			{name: "legacy create changed", mode: "create", output: "AZURE_CONTAINER_REGISTRY_ENDPOINT",
				selector: testInitACRID + "other", wantError: true},
			{name: "legacy create connection output", mode: "create", output: "AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
				wantError: true},
			{name: "legacy reuse changed", mode: "reuse-connect", output: "AZURE_CONTAINER_REGISTRY_ENDPOINT",
				selector: testInitACRID + "other", wantError: true},
			{name: "legacy reuse pull output", mode: "reuse-connect", output: "AZD_FOUNDRY_ACR_PULL_ASSIGNED",
				selector: testInitACRID + "other", wantError: true},
			{name: "legacy stable reuse", mode: "reuse-connect", output: "AZURE_CONTAINER_REGISTRY_ENDPOINT",
				selector: strings.ToUpper(testInitACRID)},
			{name: "canonical create matching", mode: "create", canonical: true, wantError: true},
			{name: "canonical reuse changed", mode: "reuse-connect", canonical: true,
				selector: testInitACRID + "other", wantError: true},
			{name: "canonical stable reuse", mode: "reuse-connect", canonical: true},
			{name: "override undeployed create", mode: "create", selector: testInitACRID + "other"},
			{name: "override undeployed reuse", mode: "reuse-connect", selector: testInitACRID + "other"},
			{name: "registry output without project identity", mode: "create",
				output: "AZURE_CONTAINER_REGISTRY_ENDPOINT", noProjectID: true, selector: testInitACRID + "other"},
		} {
			t.Run(route+"/"+tt.name, func(t *testing.T) {
				t.Setenv(synthesis.ACRReuseEnvVar, "true")
				root := t.TempDir()
				t.Chdir(root)
				content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n")
				source := []byte("print('hello')\n")
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(root, "main.py"), source, 0o600))
				templateDir := t.TempDir()
				stagedContent := append(append([]byte(nil), content...), []byte(
					"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n")...)
				require.NoError(t, os.WriteFile(filepath.Join(templateDir, "azure.yaml"), stagedContent, 0o600))
				values := map[string]string{
					"AZURE_SUBSCRIPTION_ID":                "sub",
					"AZURE_RESOURCE_GROUP":                 "foundry-rg",
					"AZURE_AI_PROJECT_ID":                  projectID,
					"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
					"AZD_FOUNDRY_ACR_MODE":                 tt.mode,
				}
				if tt.noProjectID {
					delete(values, "AZURE_AI_PROJECT_ID")
				}
				if tt.output != "" {
					values[tt.output] = map[string]string{
						"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "shared.azurecr.io",
						"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": "project-registry",
						"AZD_FOUNDRY_ACR_PULL_ASSIGNED":        "true",
					}[tt.output]
				}
				environments := &reuseInitACREnvironmentServer{testEnvironmentServiceServer: testEnvironmentServiceServer{
					environments: map[string]*azdext.Environment{"test": {Name: "test"}},
					current:      &azdext.Environment{Name: "test"},
					values:       map[string]map[string]string{"test": values},
					config:       map[string][]byte{"preserved": []byte(`{"keep":true}`)},
				}}
				if tt.canonical {
					binding, err := json.Marshal(synthesis.ACRBinding{
						Version: 1, Mode: tt.mode, RegistryResourceID: testInitACRID,
						ProjectResourceID: projectID, PrincipalID: "principal",
					})
					require.NoError(t, err)
					environments.config[synthesis.ACRBindingConfigKey] = binding
				}
				before := maps.Clone(values)
				beforeConfig := maps.Clone(environments.config)
				beforeCurrent := proto.Clone(environments.current)
				projects := &recordingProjectServer{projectPath: root}
				workflow := &recordingProjectWorkflowServer{err: errors.New("stopped at project setup")}
				prompts := &sourceInitACRPromptServer{}
				account := &sourceInitACRAccountServer{}
				client := newTestAzdClient(t, environments, workflow, prompts)
				selector := valueOrDefault(tt.selector, testInitACRID)
				flags := &initFlags{
					acrResourceIDSet: true, acrResourceID: selector, env: "test", noPrompt: true,
					projectsAddHelp: testProjectsACRHelp(workflow),
				}
				var err error
				switch route {
				case "preflight":
					err = validateInitACRProject(t.Context(), client, flags)
				case "staged":
					flags.templatePointer = filepath.Join(templateDir, "azure.yaml")
					err = validateInitACRProjectContent(t.Context(), client, flags, stagedContent, templateDir, ".")
				case "subscription":
					_, err = planInitACRSubscription(t.Context(), client, flags, ".")
				case "source":
					err = executeReuseInitACR(t, environments, workflow, prompts, projects, account, true, "test",
						"--acr-resource-id", selector, "--src", ".", "--agent-name", "agent", "--protocol", "responses")
				case "azure.yaml":
					flags.templatePointer, flags.src = filepath.Join(templateDir, "azure.yaml"), "project"
					err = runInitFromAzureYaml(t.Context(), flags, client, http.DefaultClient, stagedContent, templateDir, io.Discard)
				case "repository":
					flags.templatePointer, flags.src = filepath.Join(templateDir, "azure.yaml"), "project"
					_, err = scaffoldAzdTemplateProject(
						t.Context(), client, http.DefaultClient, "project", templateDir, "test", flags, io.Discard,
					)
				}
				targetIsFresh := route == "azure.yaml" || route == "repository"
				if tt.wantError && !targetIsFresh {
					require.ErrorContains(t, err, "provisioned Foundry project")
					require.Equal(t, before, values)
					require.Empty(t, environments.setKeys)
					require.Empty(t, workflow.requests)
					require.Empty(t, projects.added)
					require.Empty(t, projects.configValues)
					require.Empty(t, projects.configSections)
					entries, err := os.ReadDir(root)
					require.NoError(t, err)
					require.Len(t, entries, 2)
				} else if route == "preflight" || route == "staged" || route == "subscription" {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "stopped at project setup")
					require.Len(t, workflow.requests, 1)
					if route == "source" {
						require.Len(t, projects.added, 1)
					}
				}
				require.Equal(t, beforeConfig, environments.config)
				require.True(t, proto.Equal(beforeCurrent, environments.current))
				require.Len(t, environments.environments, 1)
				require.Zero(t, prompts.calls)
				require.Zero(t, prompts.locationCalls)
				require.Empty(t, account.subscriptions)
				require.NoDirExists(t, filepath.Join(root, ".azure"))
				require.NoDirExists(t, filepath.Join(root, "project"))
				for path, expected := range map[string][]byte{
					filepath.Join(root, "azure.yaml"):        content,
					filepath.Join(root, "main.py"):           source,
					filepath.Join(templateDir, "azure.yaml"): stagedContent,
				} {
					actual, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, expected, actual)
				}
			})
		}
	}
}

func TestInitACRBindingFallbackPreflight(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, tt := range []struct {
		name        string
		bindingMode string
		values      map[string]string
		selector    string
		invalid     bool
		wantError   string
	}{
		{name: "omitted aliases"},
		{name: "omitted alias with reuse mode",
			values: map[string]string{"AZD_FOUNDRY_ACR_MODE": "reuse-connect"}},
		{name: "omitted create aliases", bindingMode: "create"},
		{name: "invalid binding", invalid: true, wantError: "invalid deployed ACR binding"},
		{name: "conflicting mode", values: map[string]string{"AZD_FOUNDRY_ACR_MODE": "create"},
			wantError: "migration"},
		{name: "conflicting registry", values: map[string]string{
			"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID + "other",
		}, wantError: "migration"},
		{name: "explicit selector with conflicting mode", selector: testInitACRID,
			values: map[string]string{"AZD_FOUNDRY_ACR_MODE": "create"}, wantError: "migration"},
		{name: "explicit selector with conflicting alias", selector: testInitACRID,
			values:    map[string]string{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID + "other"},
			wantError: "migration"},
		{name: "matching registry spelling", values: map[string]string{
			"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": strings.ToUpper(testInitACRID),
		}},
		{name: "retained resource group", values: map[string]string{"AZURE_RESOURCE_GROUP": "registry-rg"},
			wantError: "azd down deletes"},
		{name: "different subscription", values: map[string]string{"AZURE_SUBSCRIPTION_ID": "other"},
			wantError: "same subscription"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0o600))
			mode := tt.bindingMode
			if mode == "" {
				mode = "reuse-connect"
			}
			binding, err := json.Marshal(synthesis.ACRBinding{
				Version: 1, Mode: mode, RegistryResourceID: testInitACRID,
				ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
					"Microsoft.CognitiveServices/accounts/account/projects/project",
				PrincipalID: "principal",
			})
			require.NoError(t, err)
			if tt.invalid {
				binding = []byte("{")
			}
			values := map[string]string{
				"AZURE_SUBSCRIPTION_ID": "sub",
				"AZURE_RESOURCE_GROUP":  "foundry-rg",
			}
			maps.Copy(values, tt.values)
			before := maps.Clone(values)
			environments := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": values},
				},
				binding: binding,
			}
			workflow := &recordingProjectWorkflowServer{}
			client := newTestAzdClient(t, environments, workflow)

			flags := &initFlags{
				acrResourceIDSet: tt.selector != "", acrResourceID: tt.selector,
			}
			err = validateInitACRProject(t.Context(), client, flags)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
				if mode == "reuse-connect" {
					require.Equal(t, testInitACRID, flags.effectiveACRResourceID)
				} else {
					require.Empty(t, flags.effectiveACRResourceID)
				}
			}
			require.Equal(t, 1, environments.bindingReads)
			require.Equal(t, before, values)
			require.Empty(t, environments.setKeys)
			require.Empty(t, workflow.requests)
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, after)
			require.NoDirExists(t, filepath.Join(root, ".azure"))
		})
	}
}

func TestInitACRRejectsCrossSubscriptionBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, template := range []string{"", "template.yaml"} {
		t.Run(template, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  foundry:\n" +
				"    host: azure.ai.project\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0o600))
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_SUBSCRIPTION_ID": "other-subscription",
						"AZURE_RESOURCE_GROUP":  "foundry-rg",
					}},
				},
			}
			client := newTestAzdClient(t, server, &recordingProjectWorkflowServer{})
			err := validateInitACRProject(t.Context(), client, &initFlags{
				acrResourceIDSet: true, acrResourceID: testInitACRID, templatePointer: template,
			})
			if template == "" {
				require.ErrorContains(t, err, "same subscription")
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, server.setKeys)
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, after)
			require.NoDirExists(t, filepath.Join(root, ".azure"))
		})
	}
}

func TestInitACRRejectsTeardownScopeBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, tt := range []struct {
		name       string
		infra      string
		rootGroup  string
		layerGroup string
		wantErr    bool
	}{
		{"root group", "", "registry-rg", "", true},
		{"layer group", "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n",
			"application-rg", "registry-rg", true},
		{"other layer root", "infra:\n  layers:\n    - name: app\n      provider: bicep\n" +
			"    - name: foundry\n      provider: microsoft.foundry\n", "registry-rg", "foundry-rg", true},
		{"unused root group", "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n",
			"registry-rg", "foundry-rg", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\n" + tt.infra + "services:\n  foundry:\n" +
				"    host: azure.ai.project\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0o600))
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_SUBSCRIPTION_ID":        "sub",
						"AZURE_RESOURCE_GROUP":         tt.rootGroup,
						"AZURE_FOUNDRY_RESOURCE_GROUP": tt.layerGroup,
					}},
				},
			}

			client := newTestAzdClient(t, server, &recordingProjectWorkflowServer{})
			err := validateInitACRProject(t.Context(), client, &initFlags{
				acrResourceIDSet: true, acrResourceID: testInitACRID,
			})
			if tt.wantErr {
				require.ErrorContains(t, err, "resource group")
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, server.setKeys)
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, after)
		})
	}
}

func TestInitACREnvironmentSelectorPreflight(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, tt := range []struct {
		name        string
		layer       bool
		application bool
		rootGroup   string
		layerGroup  string
		registry    string
		binding     bool
		wantErr     bool
	}{
		{name: "retained root group", rootGroup: "registry-rg", registry: testInitACRID, wantErr: true},
		{name: "external root registry", rootGroup: "foundry-rg", registry: testInitACRID},
		{name: "retained layer group", layer: true, rootGroup: "application-rg",
			layerGroup: "registry-rg", registry: testInitACRID, wantErr: true},
		{name: "unused root registry", layer: true, rootGroup: "registry-rg",
			layerGroup: "foundry-rg", registry: testInitACRID},
		{name: "application group", layer: true, application: true, rootGroup: "registry-rg",
			layerGroup: "foundry-rg", registry: testInitACRID, wantErr: true},
		{name: "canonical application group", layer: true, application: true, rootGroup: "registry-rg",
			layerGroup: "foundry-rg", registry: testInitACRID, binding: true, wantErr: true},
		{name: "default application group", layer: true, application: true,
			registry: strings.Replace(testInitACRID, "/registry-rg/", "/rg-test/", 1), wantErr: true},
		{name: "default root group", registry: strings.Replace(testInitACRID, "/registry-rg/", "/rg-test/", 1),
			wantErr: true},
		{name: "default layer group", layer: true,
			registry: strings.Replace(testInitACRID, "/registry-rg/", "/rg-test-foundry/", 1), wantErr: true},
		{name: "canonical retained root group", rootGroup: "registry-rg", registry: testInitACRID,
			binding: true, wantErr: true},
		{name: "canonical external root registry", rootGroup: "foundry-rg", registry: testInitACRID, binding: true},
		{name: "canonical default root group", binding: true,
			registry: strings.Replace(testInitACRID, "/registry-rg/", "/rg-test/", 1), wantErr: true},
		{name: "canonical default layer group", layer: true, binding: true,
			registry: strings.Replace(testInitACRID, "/registry-rg/", "/rg-test-foundry/", 1), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := "name: test\nservices:\n  foundry:\n    host: azure.ai.project\n"
			if tt.layer {
				content += "infra:\n  layers:\n"
				if tt.application {
					content += "    - name: app\n      provider: bicep\n"
				}
				content += "    - name: foundry\n      provider: microsoft.foundry\n"
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(content), 0o600))
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_SUBSCRIPTION_ID":                "sub",
						"AZURE_LOCATION":                       "eastus2",
						"AZURE_RESOURCE_GROUP":                 tt.rootGroup,
						"AZURE_FOUNDRY_RESOURCE_GROUP":         tt.layerGroup,
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": tt.registry,
						"AZD_FOUNDRY_ACR_MODE":                 "reuse-connect",
					}},
				},
			}
			if tt.binding {
				binding, err := json.Marshal(synthesis.ACRBinding{
					Version: 1, Mode: "reuse-connect", RegistryResourceID: tt.registry,
					ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
						"Microsoft.CognitiveServices/accounts/account/projects/project",
					PrincipalID: "principal",
				})
				require.NoError(t, err)
				server.binding = binding
				delete(server.values["test"], "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
				delete(server.values["test"], "AZD_FOUNDRY_ACR_MODE")
			}
			before := maps.Clone(server.values["test"])
			workflow := &recordingProjectWorkflowServer{}
			client := newTestAzdClient(t, server, workflow)
			err := validateInitACRProject(t.Context(), client, &initFlags{})
			if tt.wantErr {
				require.ErrorContains(t, err, "azd down deletes")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, before, server.values["test"])
			require.Empty(t, server.setKeys)
			require.Empty(t, workflow.requests)
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, string(after))
		})
	}
}

func TestInitACRStagedEnvironmentSelectorPreflight(t *testing.T) {
	for _, canonical := range []bool{false, true} {
		t.Run(fmt.Sprint("canonical=", canonical), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0o600))
			stagingDir := t.TempDir()
			stagedContent := append([]byte("infra:\n  layers:\n    - name: foundry\n"+
				"      provider: microsoft.foundry\n"), content...)
			stagedContent = append(stagedContent, []byte(
				"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n")...)
			require.NoError(t, os.WriteFile(filepath.Join(stagingDir, "azure.yaml"), stagedContent, 0o600))
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_SUBSCRIPTION_ID":                "sub",
						"AZURE_RESOURCE_GROUP":                 "application-rg",
						"AZURE_FOUNDRY_RESOURCE_GROUP":         "registry-rg",
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
						"AZD_FOUNDRY_ACR_MODE":                 "reuse-connect",
					}},
				},
			}
			if canonical {
				binding, err := json.Marshal(synthesis.ACRBinding{
					Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
					ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
						"Microsoft.CognitiveServices/accounts/account/projects/project",
					PrincipalID: "principal",
				})
				require.NoError(t, err)
				server.binding = binding
				delete(server.values["test"], "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
				delete(server.values["test"], "AZD_FOUNDRY_ACR_MODE")
			}
			before := maps.Clone(server.values["test"])
			bindingBefore := string(server.binding)
			workflow := &recordingProjectWorkflowServer{}
			client := newTestAzdClient(t, server, workflow)
			flags := &initFlags{templatePointer: "template.yaml", src: "project", env: "test", noPrompt: true}
			t.Setenv(synthesis.ACRReuseEnvVar, "false")
			requireACRFeatureDisabled(t, validateInitACRProject(t.Context(), client, flags))
			requireACRFeatureDisabled(t, validateInitACRProjectContent(t.Context(), client, flags, stagedContent, stagingDir, "."))
			t.Setenv(synthesis.ACRReuseEnvVar, "true")
			require.NoError(t, validateInitACRProject(t.Context(), client, flags))
			err := validateInitACRProjectContent(t.Context(), client, flags, stagedContent, stagingDir, ".")
			require.ErrorContains(t, err, "azd down deletes")
			workflow.err = errors.New("stopped at core init")
			for _, route := range []string{"azure.yaml", "repository"} {
				for _, enabled := range []bool{false, true} {
					t.Run(route+"/"+fmt.Sprint(enabled), func(t *testing.T) {
						t.Setenv(synthesis.ACRReuseEnvVar, map[bool]string{false: "false", true: "true"}[enabled])
						var err error
						switch route {
						case "azure.yaml":
							err = runInitFromAzureYaml(
								t.Context(), flags, client, http.DefaultClient, stagedContent, stagingDir, io.Discard,
							)
						case "repository":
							_, err = scaffoldAzdTemplateProject(
								t.Context(), client, http.DefaultClient, "project", stagingDir, "test", flags, io.Discard,
							)
						}
						require.ErrorContains(t, err, "stopped at core init")
						require.Empty(t, flags.effectiveACRResourceID)
						require.NoDirExists(t, filepath.Join(root, "project"))
					})
				}
			}
			require.Equal(t, before, server.values["test"])
			require.Equal(t, bindingBefore, string(server.binding))
			require.Empty(t, server.setKeys)
			require.Len(t, workflow.requests, 4)
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, after)
			require.NoDirExists(t, filepath.Join(root, ".azure"))
		})
	}
}

func TestInitACREnvironmentSelectorRejectsBeforeSourceInit(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, canonical := range []bool{false, true} {
		t.Run(fmt.Sprint("canonical=", canonical), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0o600))
			source := filepath.Join(root, "src")
			require.NoError(t, os.Mkdir(source, 0o750))
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": {
						"AZURE_SUBSCRIPTION_ID":                "sub",
						"AZURE_LOCATION":                       "eastus2",
						"AZURE_RESOURCE_GROUP":                 "registry-rg",
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
					}},
				},
			}
			if canonical {
				binding, err := json.Marshal(synthesis.ACRBinding{
					Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
					ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
						"Microsoft.CognitiveServices/accounts/account/projects/project",
					PrincipalID: "principal",
				})
				require.NoError(t, err)
				server.binding = binding
				delete(server.values["test"], "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
			}
			before := maps.Clone(server.values["test"])
			workflow := &recordingProjectWorkflowServer{}
			t.Setenv("AZD_SERVER", newTestAzdServer(t, server, workflow))
			cmd := newInitCommand(&azdext.ExtensionContext{NoPrompt: true, Environment: "test"})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--src", source})
			require.ErrorContains(t, cmd.Execute(), "azd down deletes")
			require.Equal(t, before, server.values["test"])
			require.Empty(t, server.setKeys)
			require.Empty(t, workflow.requests)
			//nolint:gosec // Test-owned path.
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, after)
			require.NoDirExists(t, filepath.Join(root, ".azure"))
		})
	}
}

func TestInitACREnvironmentSelectorPreservesOtherProjectFlows(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, flow := range []string{
		"explicit existing", "saved existing", "endpoint", "referenced endpoint", "legacy create",
		"canonical saved existing", "canonical endpoint",
	} {
		t.Run(flow, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := "name: test\nservices:\n  foundry:\n    host: azure.ai.project\n"
			values := map[string]string{
				"AZURE_SUBSCRIPTION_ID":                "sub",
				"AZURE_RESOURCE_GROUP":                 "registry-rg",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
			}
			flags := &initFlags{}
			switch flow {
			case "explicit existing":
				flags.projectResourceId = "existing-project"
			case "saved existing", "canonical saved existing":
				values["USE_EXISTING_AI_PROJECT"] = "true"
			case "endpoint", "referenced endpoint", "canonical endpoint":
				values["AZD_FOUNDRY_ACR_MODE"] = "already-connected"
				if flow != "referenced endpoint" {
					content += "    endpoint: https://account.services.ai.azure.com/api/projects/project\n"
				} else {
					content += "    $ref: project.yaml\n"
					require.NoError(t, os.WriteFile(filepath.Join(root, "project.yaml"), []byte(
						"endpoint: https://account.services.ai.azure.com/api/projects/project\n"), 0o600))
				}
			case "legacy create":
				values["AZURE_AI_PROJECT_ID"] = "/subscriptions/sub/resourceGroups/registry-rg/" +
					"providers/Microsoft.CognitiveServices/accounts/account/projects/project"
				values["AZURE_CONTAINER_REGISTRY_ENDPOINT"] = "shared.azurecr.io"
				values["AZD_FOUNDRY_ACR_MODE"] = "create"
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(content), 0o600))
			server := &initACREnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					values: map[string]map[string]string{"test": values},
				},
			}
			if strings.HasPrefix(flow, "canonical") {
				binding, err := json.Marshal(synthesis.ACRBinding{
					Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
					ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
						"Microsoft.CognitiveServices/accounts/account/projects/project",
					PrincipalID: "principal",
				})
				require.NoError(t, err)
				server.binding = binding
				delete(values, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
			}
			client := newTestAzdClient(t, server, &recordingProjectWorkflowServer{})
			require.NoError(t, validateInitACRProject(t.Context(), client, flags))
			require.Empty(t, server.setKeys)
		})
	}
}

func TestInitACREffectiveSelectionManifestPreflight(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, canonical := range []bool{false, true} {
		for _, tt := range []struct {
			name            string
			body            string
			image           string
			infra           string
			reference       bool
			noAgent         bool
			positional      bool
			passthrough     bool
			terraformMarker bool
			wantError       string
		}{
			{name: "source", body: "kind: hosted"},
			{name: "image defaults to source", body: "kind: hosted\n    image: registry.example.com/agent:v1"},
			{name: "unverified Bicep root", body: "kind: hosted", infra: "  provider: bicep\n",
				wantError: "Foundry-only infrastructure"},
			{name: "code", body: "kind: hosted\n    codeConfiguration:\n      runtime: python_3_13\n      entryPoint: main.py",
				wantError: "without hosted-container source builds"},
			{name: "prebuilt", body: "kind: hosted\n    image: registry.example.com/agent:v1", passthrough: true,
				wantError: "without hosted-container source builds"},
			{name: "positional prebuilt", body: "kind: hosted\n    image: registry.example.com/agent:v1",
				positional: true, passthrough: true, wantError: "without hosted-container source builds"},
			{name: "referenced prebuilt", body: "kind: hosted", image: "registry.example.com/agent:v1",
				reference: true, passthrough: true, wantError: "without hosted-container source builds"},
			{name: "referenced source", body: "kind: hosted", reference: true},
			{name: "Terraform source", body: "kind: hosted", infra: "  provider: terraform\n",
				wantError: "Foundry-only infrastructure"},
			{name: "Terraform Foundry layer", body: "kind: hosted",
				infra:     "  layers:\n    - name: foundry\n      provider: terraform\n      path: infra/foundry\n",
				wantError: "Foundry-only infrastructure"},
			{name: "Terraform application with embedded Foundry", body: "kind: hosted",
				infra: "  provider: terraform\n  layers:\n    - name: app\n      path: infra/app\n" +
					"    - name: foundry\n      provider: microsoft.foundry\n      path: infra/foundry\n",
				wantError: "Foundry-only infrastructure"},
			{name: "generated Terraform with embedded Foundry", body: "kind: hosted", terraformMarker: true,
				infra: "  layers:\n    - name: old-foundry\n      provider: terraform\n      path: infra/old-foundry\n" +
					"    - name: foundry\n      provider: microsoft.foundry\n      path: infra/foundry\n",
				wantError: "Foundry-only infrastructure"},
			{name: "Terraform application without embedded Foundry", body: "kind: hosted",
				infra: "  provider: terraform\n  layers:\n    - name: app\n      path: infra/app\n" +
					"    - name: foundry\n      provider: bicep\n      path: infra/foundry\n",
				wantError: "Foundry-only infrastructure"},
			{name: "project-only pending intent", noAgent: true},
		} {
			t.Run(fmt.Sprintf("canonical=%t/%s", canonical, tt.name), func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				content := "name: test\nservices:\n  foundry:\n    host: azure.ai.project\n"
				if !tt.noAgent {
					content += "  agent:\n    host: azure.ai.agent\n    name: test-agent\n    "
					if tt.reference {
						content += "$ref: agent.yaml\n"
						require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"), []byte(tt.body+"\n"), 0o600))
					} else {
						content += tt.body + "\n"
					}
					if tt.image != "" {
						content += "    image: " + tt.image + "\n"
					}
					if tt.passthrough {
						content += "    docker: {imagePassthrough: true}\n"
					}
				}
				if tt.infra != "" {
					content += "infra:\n" + tt.infra
				}
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(content), 0o600))
				marker := filepath.Join(root, "infra", "old-foundry", foundryTerraformMarker)
				if tt.terraformMarker {
					require.NoError(t, os.MkdirAll(filepath.Dir(marker), 0o750))
					require.NoError(t, os.WriteFile(marker, []byte(foundryTerraformV1), 0o600))
				}
				values := map[string]string{
					"AZURE_SUBSCRIPTION_ID":                "sub",
					"AZURE_RESOURCE_GROUP":                 "foundry-rg",
					"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
					"AZD_FOUNDRY_ACR_MODE":                 "reuse-connect",
				}
				environments := &reuseInitACREnvironmentServer{testEnvironmentServiceServer: testEnvironmentServiceServer{
					environments: map[string]*azdext.Environment{"test": {Name: "test"}},
					current:      &azdext.Environment{Name: "test"},
					values:       map[string]map[string]string{"test": values},
					config:       map[string][]byte{},
				}}
				if canonical {
					binding, err := json.Marshal(synthesis.ACRBinding{
						Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
						ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
							"Microsoft.CognitiveServices/accounts/account/projects/project",
						PrincipalID: "principal",
					})
					require.NoError(t, err)
					environments.config[synthesis.ACRBindingConfigKey] = binding
					delete(values, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
					delete(values, "AZD_FOUNDRY_ACR_MODE")
				}
				beforeValues := maps.Clone(values)
				beforeConfig := maps.Clone(environments.config)
				beforeCurrent := proto.Clone(environments.current)
				project := &helpersProjectServer{project: &azdext.ProjectConfig{
					Path: root,
					Services: map[string]*azdext.ServiceConfig{
						"agent": inlineAgentService(t, "agent", "test-agent"),
					},
				}}
				beforeProject := proto.Clone(project.project)
				workflow := &recordingProjectWorkflowServer{}
				if tt.noAgent {
					client := newTestAzdClient(t, environments, workflow)
					require.NoError(t, validateInitACRProject(t.Context(), client, &initFlags{}))
				} else {
					var args []string
					if tt.positional {
						args = []string{"."}
					}
					err := executeReuseInitACR(t, environments, workflow, &reuseInitACRPromptServer{},
						project, &sourceInitACRAccountServer{}, true, "", args...)
					if tt.wantError != "" {
						require.ErrorContains(t, err, tt.wantError)
					} else {
						require.NoError(t, err)
					}
				}
				require.Equal(t, beforeValues, values)
				require.Equal(t, beforeConfig, environments.config)
				require.True(t, proto.Equal(beforeCurrent, environments.current))
				require.True(t, proto.Equal(beforeProject, project.project))
				require.Empty(t, environments.setKeys)
				require.Empty(t, workflow.requests)
				after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
				require.NoError(t, err)
				require.Equal(t, content, string(after))
				if tt.reference {
					after, err = os.ReadFile(filepath.Join(root, "agent.yaml"))
					require.NoError(t, err)
					require.Equal(t, tt.body+"\n", string(after))
				}
				if tt.terraformMarker {
					after, err = os.ReadFile(marker)
					require.NoError(t, err)
					require.Equal(t, foundryTerraformV1, string(after))
				}
				require.NoDirExists(t, filepath.Join(root, ".azure"))
			})
		}
	}
}
