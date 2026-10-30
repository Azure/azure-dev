// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testInitACRID = "/subscriptions/sub/resourceGroups/registry-rg/providers/" +
	"Microsoft.ContainerRegistry/registries/shared"

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
	t.Parallel()
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
	t.Parallel()
	for _, tt := range []struct {
		name    string
		service string
		wantErr bool
	}{
		{"source", "kind: hosted", false},
		{"image", "kind: hosted\n    image: registry.example.com/agent:v1", true},
		{"prompt", "kind: prompt", true},
		{"voice", "kind: prompt-voice", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n" +
				"  agent:\n    host: azure.ai.agent\n    " + tt.service + "\n")
			err := validateInitACRManifest(content, root, &initFlags{
				acrResourceIDSet: true, acrResourceID: testInitACRID,
			})
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
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"),
		[]byte("kind: prompt\n"), 0o600))
	err := validateInitACRManifest([]byte("name: test\nservices:\n  foundry:\n"+
		"    host: azure.ai.project\n  agent:\n    host: azure.ai.agent\n    $ref: agent.yaml\n"),
		root, &initFlags{acrResourceIDSet: true, acrResourceID: testInitACRID})
	require.ErrorContains(t, err, "without hosted-container source builds")
}

func TestPreflightAzdTemplateRejectsUnsupportedACRBeforeScaffolding(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, tt := range []struct {
		name       string
		properties string
	}{
		{"prebuilt", "kind: hosted\n    image: registry.example.com/agent:v1"},
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
				require.NoError(t, validateInitACRManifest(content, templateDir, &initFlags{}))
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
					err = runInitFromAzureYaml(t.Context(), flags, client, http.DefaultClient, content, templateDir)
				} else {
					_, err = scaffoldAzdTemplateProject(
						t.Context(), client, http.DefaultClient, "project", templateDir, "test", flags,
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
				}

				var err error
				if route == "azure.yaml" {
					err = runInitFromAzureYaml(t.Context(), flags, client, http.DefaultClient, content, templateDir)
				} else {
					_, err = scaffoldAzdTemplateProject(
						t.Context(), client, http.DefaultClient, "project", templateDir, "test", flags,
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
	})
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
	binding []byte
}

func (s *initACREnvironmentServer) List(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentListResponse, error) {
	return &azdext.EnvironmentListResponse{Environments: []*azdext.EnvironmentDescription{
		{Name: "test", Default: true},
	}}, nil
}

func (s *initACREnvironmentServer) GetConfig(
	context.Context, *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	return &azdext.GetConfigResponse{Found: s.binding != nil, Value: s.binding}, nil
}

func TestPlanInitACRSubscriptionUsesConfiguredSubscription(t *testing.T) {
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
			})
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
	for _, tt := range []struct {
		name     string
		mode     string
		selected string
		wantErr  bool
	}{
		{"same registry", "reuse-connect", testInitACRID, false},
		{"different registry", "reuse-connect", testInitACRID + "other", true},
		{"create to reuse", "create", testInitACRID, true},
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
			})
			if tt.wantErr {
				require.ErrorContains(t, err, "migration")
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

func TestInitACRRejectsCrossSubscriptionBeforeWrites(t *testing.T) {
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
			require.ErrorContains(t, err, "same subscription")
			require.Empty(t, server.setKeys)
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, content, after)
			require.NoDirExists(t, filepath.Join(root, ".azure"))
		})
	}
}

func TestInitACRRejectsTeardownScopeBeforeWrites(t *testing.T) {
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
		{"other layer root", "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n",
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
	for _, tt := range []struct {
		name       string
		layer      bool
		rootGroup  string
		layerGroup string
		registry   string
		wantErr    bool
	}{
		{name: "retained root group", rootGroup: "registry-rg", registry: testInitACRID, wantErr: true},
		{name: "external root registry", rootGroup: "foundry-rg", registry: testInitACRID},
		{name: "retained layer group", layer: true, rootGroup: "application-rg",
			layerGroup: "registry-rg", registry: testInitACRID, wantErr: true},
		{name: "layer can reuse root registry", layer: true, rootGroup: "registry-rg",
			layerGroup: "foundry-rg", registry: testInitACRID},
		{name: "default root group", registry: strings.Replace(testInitACRID, "/registry-rg/", "/rg-test/", 1),
			wantErr: true},
		{name: "default layer group", layer: true,
			registry: strings.Replace(testInitACRID, "/registry-rg/", "/rg-test-foundry/", 1), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := "name: test\nservices:\n  foundry:\n    host: azure.ai.project\n"
			if tt.layer {
				content += "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n"
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

func TestInitACREnvironmentSelectorRejectsBeforeSourceInit(t *testing.T) {
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
	workflow := &recordingProjectWorkflowServer{}
	t.Setenv("AZD_SERVER", newTestAzdServer(t, server, workflow))
	cmd := newInitCommand(&azdext.ExtensionContext{NoPrompt: true, Environment: "test"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--src", source})
	require.ErrorContains(t, cmd.Execute(), "azd down deletes")
	require.Empty(t, server.setKeys)
	require.Empty(t, workflow.requests)
	after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, content, after)
	require.NoDirExists(t, filepath.Join(root, ".azure"))
}

func TestInitACREnvironmentSelectorPreservesOtherProjectFlows(t *testing.T) {
	for _, flow := range []string{
		"explicit existing", "saved existing", "endpoint", "referenced endpoint", "legacy create",
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
			case "saved existing":
				values["USE_EXISTING_AI_PROJECT"] = "true"
			case "endpoint", "referenced endpoint":
				values["AZD_FOUNDRY_ACR_MODE"] = "already-connected"
				if flow == "endpoint" {
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
			client := newTestAzdClient(t, server, &recordingProjectWorkflowServer{})
			require.NoError(t, validateInitACRProject(t.Context(), client, flags))
			require.Empty(t, server.setKeys)
		})
	}
}
