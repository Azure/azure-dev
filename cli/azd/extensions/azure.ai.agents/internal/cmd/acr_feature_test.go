// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.projects/synthesis"
	"azureaiagent/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func requireACRFeatureDisabled(t *testing.T, err error) {
	t.Helper()
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, "feature_not_enabled", local.Code, "%s", local.Message)
	require.Equal(t, azdext.LocalErrorCategoryCompatibility, local.Category)
	require.Contains(t, local.Message, synthesis.ACRReuseEnvVar+"=true")
}

func TestInitACRDisabledBeforeDaemon(t *testing.T) {
	clearPreviewProcess(t)
	root := t.TempDir()
	t.Chdir(root)
	_, err := executeRoot(t, "init", "--acr-resource-id", testInitACRID, "--no-prompt")
	requireACRFeatureDisabled(t, err)
	require.NoFileExists(t, filepath.Join(root, "azure.yaml"))
	require.NoDirExists(t, filepath.Join(root, ".azure"))
}

func acrFeatureEnvironment(t *testing.T, state string) *initACREnvironmentServer {
	t.Helper()
	env := &initACREnvironmentServer{testEnvironmentServiceServer: testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"},
		values: map[string]map[string]string{"test": {
			"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_RESOURCE_GROUP": "foundry-rg",
		}},
	}}
	switch state {
	case "selector":
		env.values["test"]["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testInitACRID
	case "canonical":
		binding, err := json.Marshal(synthesis.ACRBinding{
			Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
			ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/" +
				"providers/Microsoft.CognitiveServices/accounts/account/projects/project",
			PrincipalID: "principal",
		})
		require.NoError(t, err)
		env.binding = binding
	case "pending":
		env.pending = []byte(`{"version":1}`)
	}
	return env
}

func TestInitACRDisabledWithoutFlagPreservesState(t *testing.T) {
	for _, state := range []string{"selector", "canonical", "pending"} {
		t.Run(state, func(t *testing.T) {
			clearPreviewProcess(t)
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  project:\n    host: azure.ai.project\n")
			path := filepath.Join(root, "azure.yaml")
			require.NoError(t, os.WriteFile(path, content, 0o600))
			source := filepath.Join(root, "src")
			require.NoError(t, os.Mkdir(source, 0o750))
			env := acrFeatureEnvironment(t, state)
			before := maps.Clone(env.values["test"])
			workflow := &recordingProjectWorkflowServer{}
			t.Setenv("AZD_SERVER", newTestAzdServer(t, env, workflow))
			_, err := executeRoot(t, "init", "--src", source, "--no-prompt")
			requireACRFeatureDisabled(t, err)
			require.Equal(t, before, env.values["test"])
			require.Empty(t, env.setKeys)
			require.Empty(t, workflow.requests)
			require.NoDirExists(t, filepath.Join(root, ".azure"))
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, content, after)
		})
	}
}

func TestACRDisabledEjectAndHooksPreserveState(t *testing.T) {
	for _, state := range []string{"selector", "canonical", "pending"} {
		for _, operation := range []string{"eject", "preprovision", "predeploy", "predown"} {
			t.Run(state+"/"+operation, func(t *testing.T) {
				clearPreviewProcess(t)
				root := t.TempDir()
				t.Chdir(root)
				content := []byte(validFoundryAzureYAML)
				path := filepath.Join(root, "azure.yaml")
				require.NoError(t, os.WriteFile(path, content, 0o600))
				env := acrFeatureEnvironment(t, state)
				before := maps.Clone(env.values["test"])
				workflow := &recordingProjectWorkflowServer{}
				client := newTestAzdClient(t, env, workflow)
				props, err := structpb.NewStruct(map[string]any{"kind": "hosted", "name": "agent"})
				require.NoError(t, err)
				service := &azdext.ServiceConfig{Name: "agent", Host: AiAgentHost, AdditionalProperties: props}
				project := &azdext.ProjectConfig{Path: root, Services: map[string]*azdext.ServiceConfig{"agent": service}}
				beforeProject := proto.Clone(project)
				args := &azdext.ProjectEventArgs{Project: project}
				var run func(context.Context) error
				switch operation {
				case "eject":
					run = func(ctx context.Context) error { return ejectInfraAfterInit(ctx, "bicep", client) }
				case "preprovision":
					run = func(ctx context.Context) error { return preprovisionHandler(ctx, client, args) }
				case "predown":
					run = func(ctx context.Context) error { return predownHandler(ctx, client, args) }
				case "predeploy":
					run = func(ctx context.Context) error {
						_, err := predeployHandler(ctx, client, &azdext.ServiceEventArgs{Project: project, Service: service})
						return err
					}
				}
				requireACRFeatureDisabled(t, run(t.Context()))
				require.True(t, proto.Equal(beforeProject, project))
				require.Equal(t, before, env.values["test"])
				require.Empty(t, env.setKeys)
				require.Empty(t, workflow.requests)
				require.NoDirExists(t, filepath.Join(root, "infra"))
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, content, after)
			})
		}
	}
}

func TestACRParameterOnlyHooksPreserveState(t *testing.T) {
	for _, layer := range []bool{false, true} {
		for _, format := range []string{"json", "bicepparam"} {
			for _, operation := range []string{"preprovision", "predeploy"} {
				name := map[bool]string{false: "root", true: "layer"}[layer] + "/" + format + "/" + operation
				t.Run(name, func(t *testing.T) {
					clearPreviewProcess(t)
					root := t.TempDir()
					t.Chdir(root)
					testutil.ACRParameters(t, root, layer, format, testInitACRID)
					env := acrFeatureEnvironment(t, "")
					beforeValues := maps.Clone(env.values["test"])
					beforeFiles := testutil.Snapshot(t, root)
					configRoot := os.Getenv("AZD_CONFIG_DIR")
					beforeConfig := testutil.Snapshot(t, configRoot)
					workflow := &recordingProjectWorkflowServer{}
					client := newTestAzdClient(t, env, workflow)
					props, err := structpb.NewStruct(map[string]any{"kind": "hosted", "name": "agent"})
					require.NoError(t, err)
					service := &azdext.ServiceConfig{Name: "agent", Host: AiAgentHost, AdditionalProperties: props}
					project := &azdext.ProjectConfig{Path: root, Services: map[string]*azdext.ServiceConfig{"agent": service}}
					beforeProject := proto.Clone(project)
					switch operation {
					case "preprovision":
						err = preprovisionHandler(t.Context(), client, &azdext.ProjectEventArgs{Project: project})
					case "predeploy":
						_, err = predeployHandler(t.Context(), client, &azdext.ServiceEventArgs{Project: project, Service: service})
					}
					requireACRFeatureDisabled(t, err)
					require.True(t, proto.Equal(beforeProject, project))
					require.Equal(t, beforeValues, env.values["test"])
					require.Empty(t, env.setKeys)
					require.Equal(t, 1, env.getCurrentCalls)
					require.Equal(t, 1, env.getValuesCalls)
					require.Empty(t, workflow.requests)
					require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
					require.Equal(t, beforeConfig, testutil.Snapshot(t, configRoot))
				})
			}
		}
	}
}

func TestACRParameterOnlyPredownDoesNotCompile(t *testing.T) {
	for _, layer := range []bool{false, true} {
		for _, format := range []string{"json", "bicepparam"} {
			for _, id := range []string{"", testInitACRID} {
				name := map[bool]string{false: "root", true: "layer"}[layer] + "/" + format +
					"/" + map[bool]string{false: "create", true: "source-only reuse"}[id != ""]
				t.Run(name, func(t *testing.T) {
					clearPreviewProcess(t)
					root := t.TempDir()
					testutil.ACRParameters(t, root, layer, format, id)
					t.Setenv("AZD_BICEP_TOOL_PATH", filepath.Join(root, "missing-bicep"))
					env := acrFeatureEnvironment(t, "")
					beforeValues, beforeFiles := maps.Clone(env.values["test"]), testutil.Snapshot(t, root)
					client := newTestAzdClient(t, env, &recordingProjectWorkflowServer{})
					props, err := structpb.NewStruct(map[string]any{"kind": "hosted", "name": "agent"})
					require.NoError(t, err)
					service := &azdext.ServiceConfig{Name: "agent", Host: AiAgentHost, AdditionalProperties: props}
					project := &azdext.ProjectConfig{
						Path: root, Services: map[string]*azdext.ServiceConfig{"agent": service},
					}
					require.NoError(t, predownHandler(t.Context(), client, &azdext.ProjectEventArgs{Project: project}))
					require.Equal(t, beforeValues, env.values["test"])
					require.Empty(t, env.setKeys)
					require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
				})
			}
		}
	}
}

func TestACRParameterOnlyHookPositivePaths(t *testing.T) {
	for _, policy := range []struct {
		name, id, flag string
	}{{"enabled reuse", testInitACRID, "true"}, {"disabled create", "", ""}} {
		for _, layer := range []bool{false, true} {
			for _, format := range []string{"json", "bicepparam"} {
				for _, operation := range []string{"preprovision", "predeploy", "predown"} {
					name := policy.name + "/" + map[bool]string{false: "root", true: "layer"}[layer] +
						"/" + format + "/" + operation
					t.Run(name, func(t *testing.T) {
						clearPreviewProcess(t)
						t.Setenv(synthesis.ACRReuseEnvVar, policy.flag)
						root := t.TempDir()
						t.Chdir(root)
						testutil.ACRParameters(t, root, layer, format, policy.id)
						env := acrFeatureEnvironment(t, "")
						client := newTestAzdClient(t, env, &recordingProjectWorkflowServer{})
						props, err := structpb.NewStruct(map[string]any{"kind": "hosted", "name": "agent"})
						require.NoError(t, err)
						service := &azdext.ServiceConfig{Name: "agent", Host: AiAgentHost, AdditionalProperties: props}
						project := &azdext.ProjectConfig{Path: root, Services: map[string]*azdext.ServiceConfig{"agent": service}}
						switch operation {
						case "preprovision":
							err = preprovisionHandler(t.Context(), client, &azdext.ProjectEventArgs{Project: project})
						case "predeploy":
							_, err = predeployHandler(t.Context(), client, &azdext.ServiceEventArgs{Project: project, Service: service})
						case "predown":
							err = predownHandler(t.Context(), client, &azdext.ProjectEventArgs{Project: project})
						}
						require.NoError(t, err)
					})
				}
			}
		}
	}
}

func TestACRHelpAndStaticMetadata(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			clearPreviewProcess(t)
			if enabled {
				t.Setenv(synthesis.ACRReuseEnvVar, "true")
			}
			root := NewRootCommand()
			init, _, err := root.Find([]string{"init"})
			require.NoError(t, err)
			require.False(t, init.Flags().Lookup("acr-resource-id").Hidden)
			require.Contains(t, init.Long, "--acr-resource-id")
			text, err := executeRoot(t, "init", "--help")
			require.NoError(t, err)
			require.Equal(t, enabled, strings.Contains(text, "--acr-resource-id"))
		})
	}
}
