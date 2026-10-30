// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func requireACRFeatureDisabled(t *testing.T, err error) {
	t.Helper()
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, "feature_not_enabled", local.Code)
	require.Equal(t, azdext.LocalErrorCategoryCompatibility, local.Category)
	require.Contains(t, local.Message, synthesis.ACRReuseEnvVar+"=true")
}

func TestProjectAddACRDisabledBeforeDaemon(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("AZD_SERVER", "")
	command := newProjectAddCommand(&azdext.ExtensionContext{NoPrompt: true})
	require.NoError(t, command.ParseFlags([]string{"--new-project", "--acr-resource-id", testExternalRegistryID}))
	requireACRFeatureDisabled(t, command.RunE(command, nil))
	require.NoFileExists(t, filepath.Join(root, "azure.yaml"))
	require.NoDirExists(t, filepath.Join(root, ".azure"))
}

func TestProjectAddACRDisabledPreservesState(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	for _, state := range []string{"selector", "canonical without aliases", "pending without aliases"} {
		for _, infra := range []string{"", "bicep"} {
			t.Run(state+"/"+infra, func(t *testing.T) {
				root := t.TempDir()
				t.Setenv("AZD_EXEC_PROJECT_DIR", root)
				raw := []byte("name: test\ninfra:\n  provider: microsoft.foundry\nservices:\n" +
					"  project:\n    host: azure.ai.project\n")
				path := filepath.Join(root, "azure.yaml")
				require.NoError(t, os.WriteFile(path, raw, 0o600))
				client, project, env, models, workflow := newSelfInitializingDeploymentClient(t, root)
				env.config = map[string][]byte{}
				switch state {
				case "selector":
					env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testExternalRegistryID
				case "canonical without aliases":
					binding, err := json.Marshal(synthesis.ACRBinding{
						Version: 1, Mode: "reuse-connect", RegistryResourceID: testExternalRegistryID,
						ProjectResourceID: "/subscriptions/subscription/resourceGroups/project/" +
							"providers/Microsoft.CognitiveServices/accounts/account/projects/project",
						PrincipalID: "principal",
					})
					require.NoError(t, err)
					env.config[synthesis.ACRBindingConfigKey] = binding
				case "pending without aliases":
					env.config[synthesis.ACRPendingConfigKey] = []byte(`{"version":1}`)
				}
				beforeValues, beforeConfig := maps.Clone(env.values), maps.Clone(env.config)
				action := &ProjectAddAction{
					client: client, flags: &projectAddFlags{noPrompt: true, infra: infra},
					extCtx: &azdext.ExtensionContext{Environment: "test"},
				}
				requireACRFeatureDisabled(t, action.Run(t.Context()))
				require.Equal(t, beforeValues, env.values)
				require.Equal(t, beforeConfig, env.config)
				require.Empty(t, workflow.args)
				require.Zero(t, models.calls)
				require.False(t, project.providerSet)
				require.False(t, project.deploymentSet)
				// #nosec G304 -- isolated test fixture.
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, raw, after)
				require.NoDirExists(t, filepath.Join(root, "infra"))
				require.NoDirExists(t, filepath.Join(root, ".azure"))
			})
		}
	}
}

func TestProjectACRHelpAndStaticMetadata(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			value := ""
			if enabled {
				value = "true"
			}
			t.Setenv(synthesis.ACRReuseEnvVar, value)
			root := NewRootCommand()
			add, _, err := root.Find([]string{"add"})
			require.NoError(t, err)
			require.False(t, add.Flags().Lookup("acr-resource-id").Hidden)
			require.Contains(t, add.Long, "--acr-resource-id")
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetArgs([]string{"add", "--help"})
			require.NoError(t, root.Execute())
			require.Equal(t, enabled, bytes.Contains(output.Bytes(), []byte("--acr-resource-id")))
		})
	}
}
