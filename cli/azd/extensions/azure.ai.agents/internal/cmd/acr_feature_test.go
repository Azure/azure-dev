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

	"azureaiagent/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func requireACRFeatureDisabled(t *testing.T, err error) {
	t.Helper()
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, "feature_not_enabled", local.Code)
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
