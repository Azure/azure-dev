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
	"testing"

	"azure.ai.projects/internal/testutil"
	"azure.ai.projects/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestProjectACRSelectedEnvironmentHooks(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	for _, state := range []string{"clean", "pending", "binding"} {
		t.Run(state, func(t *testing.T) {
			t.Setenv("AZD_ENVIRONMENT", "selected")
			root := t.TempDir()
			raw := []byte("infra:\n  provider: microsoft.foundry\nservices:\n  project:\n    host: azure.ai.project\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), raw, 0o600))
			server := &recordingProjectEnvironmentServer{
				envName: "selected", values: map[string]string{"AZURE_SUBSCRIPTION_ID": "sub"},
				currentErr: status.Error(codes.Unknown, "default environment not found"),
				config:     map[string][]byte{},
			}
			switch state {
			case "pending":
				server.config[synthesis.ACRPendingConfigKey] = []byte(`{"version":1}`)
			case "binding":
				binding, err := json.Marshal(synthesis.ACRBinding{
					Version: 1, Mode: "reuse-connect", RegistryResourceID: testExternalRegistryID,
					ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry/" +
						"providers/Microsoft.CognitiveServices/accounts/account/projects/project",
					PrincipalID: "principal",
				})
				require.NoError(t, err)
				server.config[synthesis.ACRBindingConfigKey] = binding
			}
			client := newProjectEnvironmentClient(t, server)
			project := &azdext.ProjectConfig{Path: root, Services: map[string]*azdext.ServiceConfig{
				"project": {Name: "project", Host: aiProjectHost},
			}}
			for _, check := range []func(context.Context, *azdext.AzdClient, *azdext.ProjectConfig) error{
				synthesis.RequireACRReuseForProject, synthesis.RequireACRReuseForProjectState,
				func(ctx context.Context, client *azdext.AzdClient, project *azdext.ProjectConfig) error {
					return validateProjectACRBuilds(ctx, client, &azdext.ProjectEventArgs{Project: project})
				},
			} {
				err := check(t.Context(), client, project)
				if state == "clean" {
					require.NoError(t, err)
				} else {
					requireACRFeatureDisabled(t, err)
				}
			}
			if state == "clean" {
				require.NoError(t, projectLifecycleHandler(t.Context(), client,
					&azdext.ProjectEventArgs{Project: project}))
				require.Equal(t, "selected", server.envNameSet)
			} else {
				require.Empty(t, server.key)
			}
		})
	}
}

func TestProjectSelectedEnvironmentFailureDoesNotUseDefault(t *testing.T) {
	t.Setenv("AZD_ENVIRONMENT", "missing")
	for _, code := range []codes.Code{codes.NotFound, codes.Unavailable, codes.PermissionDenied} {
		t.Run(code.String(), func(t *testing.T) {
			server := &recordingProjectEnvironmentServer{
				envName: "default", namedErr: status.Error(code, "selected environment read failed"),
			}
			client := newProjectEnvironmentClient(t, server)
			_, err := synthesis.CurrentEnvironment(t.Context(), client)
			require.Equal(t, code, status.Code(err))
			project := &azdext.ProjectConfig{Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
				"project": {Name: "project", Host: aiProjectHost},
			}}
			err = validateProjectACRBuilds(t.Context(), client, &azdext.ProjectEventArgs{Project: project})
			require.Equal(t, code, status.Code(err))
			require.Empty(t, server.key)
		})
	}
	t.Setenv("AZD_ENVIRONMENT", "")
	client := newProjectEnvironmentClient(t, &recordingProjectEnvironmentServer{envName: "default"})
	current, err := synthesis.CurrentEnvironment(t.Context(), client)
	require.NoError(t, err)
	require.Equal(t, "default", current.GetEnvironment().GetName())
}

func requireACRFeatureDisabled(t *testing.T, err error) {
	t.Helper()
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, "feature_not_enabled", local.Code)
	require.Equal(t, azdext.LocalErrorCategoryCompatibility, local.Category)
	require.Contains(t, local.Message, synthesis.ACRReuseEnvVar+"=true")
}

func TestProjectACRGuardWithoutCurrentEnvironment(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	for _, test := range []struct {
		name      string
		selection string
		code      codes.Code
		message   string
	}{
		{name: "missing default", code: codes.Unknown, message: "default environment not found"},
		{name: "unavailable", code: codes.Unavailable, message: "host unavailable"},
		{name: "permission denied", code: codes.PermissionDenied, message: "access denied"},
		{name: "lookalike message", code: codes.Unknown, message: "read failed: default environment not found"},
		{name: "different code", code: codes.NotFound, message: "default environment not found"},
		{name: "named environment", selection: "selected", code: codes.Unknown, message: "default environment not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AZD_ENVIRONMENT", test.selection)
			server := &recordingProjectEnvironmentServer{
				currentErr: status.Error(test.code, test.message),
				namedErr:   status.Error(test.code, test.message),
			}
			client := newProjectEnvironmentClient(t, server)
			for _, reuse := range []bool{false, true} {
				t.Run(map[bool]string{false: "clean", true: "on-disk reuse"}[reuse], func(t *testing.T) {
					root := t.TempDir()
					id := ""
					if reuse {
						id = testExternalRegistryID
					}
					testutil.ACRParameters(t, root, false, "json", id)
					project := &azdext.ProjectConfig{Path: root}
					before := testutil.Snapshot(t, root)
					for _, stateOnly := range []bool{false, true} {
						check := synthesis.RequireACRReuseForProject
						if stateOnly {
							check = synthesis.RequireACRReuseForProjectState
						}
						err := check(t.Context(), client, project)
						if test.name != "missing default" {
							require.Equal(t, test.code, status.Code(err), "%v", err)
						} else if reuse && !stateOnly {
							requireACRFeatureDisabled(t, err)
						} else {
							require.NoError(t, err)
						}
					}
					require.Empty(t, server.key)
					require.Equal(t, before, testutil.Snapshot(t, root))
				})
			}
		})
	}
}

func TestProjectACRParameterOnlyPrepackageWithoutEnvironment(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	for _, tt := range []struct {
		name       string
		envName    string
		currentErr error
	}{
		{name: "empty environment"},
		{name: "not found", currentErr: status.Error(codes.NotFound, "environment not found")},
		{name: "no default", currentErr: status.Error(codes.Unknown, "default environment not found")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			testutil.ACRParameters(t, root, false, "json", testExternalRegistryID)
			client, project, env, _, _ := newSelfInitializingDeploymentClient(t, root)
			project.project.Services["project"] = &azdext.ServiceConfig{Name: "project", Host: aiProjectHost}
			env.current = tt.envName
			env.currentErr = tt.currentErr
			beforeFiles := testutil.Snapshot(t, root)
			requireACRFeatureDisabled(t, validateProjectACRBuilds(t.Context(), client,
				&azdext.ProjectEventArgs{Project: project.project}))
			require.Empty(t, env.setLog)
			require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
		})
	}
}

func TestProjectACRParameterOnlyPrepackage(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	for _, layer := range []bool{false, true} {
		for _, format := range []string{"json", "bicepparam"} {
			t.Run(map[bool]string{false: "root", true: "layer"}[layer]+"/"+format, func(t *testing.T) {
				root := t.TempDir()
				testutil.ACRParameters(t, root, layer, format, testExternalRegistryID)
				client, project, env, models, workflow := newSelfInitializingDeploymentClient(t, root)
				project.project.Services["project"] = &azdext.ServiceConfig{Name: "project", Host: aiProjectHost}
				beforeProject := proto.Clone(project.project)
				beforeValues, beforeConfig := maps.Clone(env.values), maps.Clone(env.config)
				beforeFiles := testutil.Snapshot(t, root)
				configRoot := os.Getenv("AZD_CONFIG_DIR")
				beforeUserConfig := testutil.Snapshot(t, configRoot)
				requireACRFeatureDisabled(t, validateProjectACRBuilds(t.Context(), client,
					&azdext.ProjectEventArgs{Project: project.project}))
				require.Empty(t, env.setLog)
				require.Equal(t, beforeValues, env.values)
				require.Equal(t, beforeConfig, env.config)
				require.True(t, proto.Equal(beforeProject, project.project))
				require.Empty(t, workflow.args)
				require.Zero(t, models.calls)
				require.False(t, project.providerSet)
				require.False(t, project.deploymentSet)
				require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
				require.Equal(t, beforeUserConfig, testutil.Snapshot(t, configRoot))
			})
		}
	}
}

func TestProjectAddACRDisabledBeforeDaemon(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("AZD_SERVER", "")
	command := newProjectAddCommand(&azdext.ExtensionContext{NoPrompt: true})
	command.SetContext(t.Context())
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

func TestProjectACRParameterOnlyLifecycle(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, layer := range []bool{false, true} {
			for _, format := range []string{"json", "bicepparam"} {
				name := map[bool]string{false: "disabled", true: "enabled"}[enabled] + "/" +
					map[bool]string{false: "root", true: "layer"}[layer] + "/" + format
				t.Run(name, func(t *testing.T) {
					t.Setenv(synthesis.ACRReuseEnvVar, map[bool]string{false: "", true: "true"}[enabled])
					t.Setenv("AZD_NO_PROMPT", "true")
					root := t.TempDir()
					testutil.ACRParameters(t, root, layer, format, testExternalRegistryID)
					client, project, env, models, workflow := newSelfInitializingDeploymentClient(t, root)
					props, err := structpb.NewStruct(map[string]any{"deployments": []any{
						map[string]any{"name": "model", "model": map[string]any{"name": "gpt-4.1"}},
					}})
					require.NoError(t, err)
					project.project.Services["project"] = &azdext.ServiceConfig{
						Name: "project", Host: aiProjectHost, AdditionalProperties: props,
					}
					beforeProject := proto.Clone(project.project)
					beforeValues, beforeConfig := maps.Clone(env.values), maps.Clone(env.config)
					beforeFiles := testutil.Snapshot(t, root)
					configRoot := os.Getenv("AZD_CONFIG_DIR")
					beforeUserConfig := testutil.Snapshot(t, configRoot)
					err = projectLifecycleHandler(t.Context(), client, &azdext.ProjectEventArgs{Project: project.project})
					if enabled {
						require.NoError(t, err)
						require.NotEmpty(t, env.setLog)
						require.Contains(t, env.values, projectDeploymentsEnvKey)
					} else {
						requireACRFeatureDisabled(t, err)
						require.Empty(t, env.setLog)
						require.Equal(t, beforeValues, env.values)
					}
					require.Equal(t, beforeConfig, env.config)
					require.True(t, proto.Equal(beforeProject, project.project))
					require.Empty(t, workflow.args)
					require.Zero(t, models.calls)
					require.False(t, project.providerSet)
					require.False(t, project.deploymentSet)
					require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
					require.Equal(t, beforeUserConfig, testutil.Snapshot(t, configRoot))
				})
			}
		}
	}
}

func TestProjectACRParameterOnlyUnnamedEnvironment(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "")
	root := t.TempDir()
	testutil.ACRParameters(t, root, false, "json", testExternalRegistryID)
	client, project, env, models, workflow := newSelfInitializingDeploymentClient(t, root)
	env.current = ""
	before := testutil.Snapshot(t, root)
	requireACRFeatureDisabled(t, synthesis.RequireACRReuseForProject(t.Context(), client, project.project))
	require.Empty(t, env.setLog)
	require.Empty(t, workflow.args)
	require.Zero(t, models.calls)
	require.Equal(t, before, testutil.Snapshot(t, root))
}

func TestProjectACRPredownUsesRecordedState(t *testing.T) {
	for _, state := range []string{"none", "selector", "canonical", "pending"} {
		t.Run(state, func(t *testing.T) {
			t.Setenv(synthesis.ACRReuseEnvVar, "")
			root := t.TempDir()
			testutil.ACRParameters(t, root, false, "bicepparam", testExternalRegistryID)
			t.Setenv("AZD_BICEP_TOOL_PATH", filepath.Join(root, "missing-bicep"))
			client, project, env, _, _ := newSelfInitializingDeploymentClient(t, root)
			if env.config == nil {
				env.config = map[string][]byte{}
			}
			switch state {
			case "selector":
				env.values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testExternalRegistryID
			case "canonical":
				binding, err := json.Marshal(synthesis.ACRBinding{
					Version: 1, Mode: "reuse-connect", RegistryResourceID: testExternalRegistryID,
					ProjectResourceID: "/subscriptions/subscription/resourceGroups/project/" +
						"providers/Microsoft.CognitiveServices/accounts/account/projects/project",
					PrincipalID: "principal",
				})
				require.NoError(t, err)
				env.config[synthesis.ACRBindingConfigKey] = binding
			case "pending":
				env.config[synthesis.ACRPendingConfigKey] = []byte(`{"version":1}`)
			}
			beforeValues, beforeConfig := maps.Clone(env.values), maps.Clone(env.config)
			beforeFiles := testutil.Snapshot(t, root)
			err := synthesis.RequireACRReuseForProjectState(t.Context(), client, project.project)
			if state == "none" {
				require.NoError(t, err)
			} else {
				requireACRFeatureDisabled(t, err)
			}
			require.Equal(t, beforeValues, env.values)
			require.Equal(t, beforeConfig, env.config)
			require.Empty(t, env.setLog)
			require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
		})
	}
}
