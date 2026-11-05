// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"azure.ai.projects/synthesis"
	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
)

func TestInitACRConflictNamesSelectorSource(t *testing.T) {
	for _, source := range []string{"--acr-resource-id", "AZURE_CONTAINER_REGISTRY_RESOURCE_ID", "saved ACR binding"} {
		t.Run(source, func(t *testing.T) {
			flags := &initFlags{
				acrResourceID: testInitACRID, acrResourceIDSet: source == "--acr-resource-id",
				effectiveACRResourceID: testInitACRID, acrSelectionSource: source, deployMode: "code",
			}
			err := validateResolvedInitACRInput(flags)
			require.ErrorContains(t, err, source+" cannot be combined with --deploy-mode code")
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
			require.Contains(t, localErr.Suggestion, "source builds")
			require.Contains(t, localErr.Suggestion, "separate azd environment")
			require.NotContains(t, localErr.Suggestion, "clear")
			require.NotContains(t, localErr.Suggestion, "remove")
		})
	}
}

func TestInitACRNormalizedTerraformBeforeWrites(t *testing.T) {
	for _, provider := range []string{" terraform", "TeRrAfOrM ", "\t TERRAFORM \n"} {
		t.Run(fmt.Sprintf("%q", provider), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			setTimedInitAuth(t)
			cmd := newInitCommand(&azdext.ExtensionContext{NoPrompt: true})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--acr-resource-id", testInitACRID, "--infra=" + provider})
			require.ErrorContains(t, cmd.Execute(), "--infra terraform")
			entries, err := os.ReadDir(root)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestInitACRConflictUsesAuthoritativeBinding(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile("azure.yaml", []byte("name: test\n"), 0o600))
	environments := &initACREnvironmentServer{testEnvironmentServiceServer: testEnvironmentServiceServer{
		values: map[string]map[string]string{"test": {
			"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
		}},
	}}
	binding, err := json.Marshal(synthesis.ACRBinding{
		Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
		ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
			"Microsoft.CognitiveServices/accounts/account/projects/project",
		PrincipalID: "principal",
	})
	require.NoError(t, err)
	environments.binding = binding
	client := newTestAzdClient(t, environments, &recordingProjectWorkflowServer{})
	err = validateInitACRProject(t.Context(), client, &initFlags{deployMode: "code"})
	require.ErrorContains(t, err, "saved ACR binding cannot be combined with --deploy-mode code")
	require.Equal(t, binding, environments.binding)
	require.Empty(t, environments.setKeys)
}

func TestInitACRTemplateRetainedSaltBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, envName := range []string{"test", strings.Repeat("a", 64)} {
		for _, layer := range []bool{false, true} {
			for _, saved := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/layer=%t/saved=%t", envName, layer, saved), func(t *testing.T) {
					root := t.TempDir()
					t.Chdir(root)
					template := t.TempDir()
					content := "name: test\nservices:\n  foundry:\n    host: azure.ai.project\n" +
						"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n"
					if layer {
						content += "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n"
					}
					require.NoError(t, os.WriteFile(filepath.Join(template, "azure.yaml"), []byte(content), 0o600))
					salt := "12345678"
					saltedGroup := composeSaltedResourceGroupName(envName, salt)
					registry := strings.Replace(testInitACRID, "registry-rg", saltedGroup, 1)
					dir := filepath.Join(root, ".azure", envName)
					require.NoError(t, os.MkdirAll(dir, 0o750))
					state := []byte("AZURE_SUBSCRIPTION_ID=sub\nAZD_RESOURCE_TOKEN_SALT=" + salt + "\n")
					if saved {
						state = append(state, []byte("AZURE_CONTAINER_REGISTRY_RESOURCE_ID="+registry+"\n")...)
					}
					envPath := filepath.Join(dir, ".env")
					require.NoError(t, os.WriteFile(envPath, state, 0o600))
					flags := &initFlags{
						templatePointer: template, env: envName, noPrompt: true,
						acrResourceIDSet: !saved, acrResourceID: registry,
					}
					environments := &testEnvironmentServiceServer{}
					workflow := &recordingProjectWorkflowServer{}
					flags.projectsAddHelp = testProjectsACRHelp(workflow)
					client := newTestAzdClient(t, environments, workflow)
					_, err := preflightAzdTemplate(t.Context(), client, http.DefaultClient, template, flags, ".", io.Discard)
					if layer {
						require.NoError(t, err)
						require.Len(t, workflow.probes, 1)
					} else {
						require.ErrorContains(t, err, "azd down deletes")
						require.Empty(t, workflow.probes)
					}
					require.Empty(t, workflow.requests)
					require.Empty(t, environments.setKeys)
					actual, err := os.ReadFile(envPath)
					require.NoError(t, err)
					require.Equal(t, state, actual)
					require.NoFileExists(t, "azure.yaml")
					planned := plannedInitACREnvironment(flags, map[string]string{
						"AZURE_ENV_NAME": envName, resourceTokenSaltKey: salt,
					})
					require.Equal(t, saltedGroup, planned[resourceGroupEnvKey])
					require.LessOrEqual(t, len(saltedGroup), 90)
				})
			}
		}
	}
}

func TestInitACRPlannedGroupNamingPreservesState(t *testing.T) {
	for _, envName := range []string{"test", strings.Repeat("a", 78) + "-.tail"} {
		values := map[string]string{"AZURE_ENV_NAME": envName, resourceTokenSaltKey: "12345678"}
		before := maps.Clone(values)
		flags := &initFlags{acrTemplateTarget: true}
		planned := plannedInitACREnvironment(flags, values)
		require.Equal(t, composeSaltedResourceGroupName(envName, "12345678"), planned[resourceGroupEnvKey])
		require.LessOrEqual(t, len(planned[resourceGroupEnvKey]), 90)
		require.Equal(t, before, values)
		values[resourceGroupEnvKey] = "authored-group"
		require.Equal(t, "authored-group", plannedInitACREnvironment(flags, values)[resourceGroupEnvKey])
		delete(values, resourceGroupEnvKey)
		require.Equal(t, values, plannedInitACREnvironment(&initFlags{}, values))
	}
}

func writeInitACRRegressionBinding(t *testing.T, path, registry string) []byte {
	t.Helper()
	state, err := json.Marshal(map[string]any{
		"extensions": map[string]any{"ai-projects": map[string]any{
			"acrBinding": synthesis.ACRBinding{
				Version: 1, Mode: "reuse-connect", RegistryResourceID: registry,
				ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
					"Microsoft.CognitiveServices/accounts/account/projects/project",
				PrincipalID: "principal",
			},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, state, 0o600))
	return state
}

func TestInitACROrphanedSourceEnvironmentBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, boundary := range []string{"subscription", "binding", "saved environment", "saved binding"} {
		t.Run(boundary, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			source := []byte("print('hello')\n")
			require.NoError(t, os.WriteFile("main.py", source, 0o600))
			dir := filepath.Join(root, ".azure", "planned")
			require.NoError(t, os.MkdirAll(dir, 0o750))
			envPath := filepath.Join(dir, ".env")
			state := []byte("AZURE_SUBSCRIPTION_ID=sub\n")
			args := []string{"--src", ".", "--agent-name", "agent", "--protocol", "responses"}
			want := "cannot be combined with --deploy-mode code"
			switch boundary {
			case "subscription":
				state = []byte("AZURE_SUBSCRIPTION_ID=other\n")
				args = append(args, "--acr-resource-id", testInitACRID)
				want = "same subscription"
			case "binding":
				args = append(args, "--acr-resource-id", strings.Replace(testInitACRID, "/shared", "/different", 1))
				want = "deployed"
			case "saved environment":
				state = append(state, []byte("AZURE_CONTAINER_REGISTRY_RESOURCE_ID="+testInitACRID+"\n")...)
				args = append(args, "--deploy-mode", "code")
			case "saved binding":
				args = append(args, "--deploy-mode", "code")
			}
			require.NoError(t, os.WriteFile(envPath, state, 0o600))
			var binding []byte
			configPath := filepath.Join(dir, "config.json")
			if boundary == "binding" || boundary == "saved binding" {
				binding = writeInitACRRegressionBinding(t, configPath, testInitACRID)
			}
			environments := &reuseInitACREnvironmentServer{testEnvironmentServiceServer: testEnvironmentServiceServer{
				current:      &azdext.Environment{Name: "unrelated"},
				environments: map[string]*azdext.Environment{"unrelated": {Name: "unrelated"}},
				values:       map[string]map[string]string{"unrelated": {"AZURE_SUBSCRIPTION_ID": "unrelated"}},
			}}
			before := maps.Clone(environments.values["unrelated"])
			workflow := &recordingProjectWorkflowServer{}
			projects := &recordingProjectServer{getProjectErr: status.Error(codes.NotFound, "no project")}
			prompts := &sourceInitACRPromptServer{}
			err := executeReuseInitACR(t, environments, workflow, prompts, projects, &sourceInitACRAccountServer{},
				true, "planned", args...)
			require.ErrorContains(t, err, want)
			require.Empty(t, workflow.requests)
			require.Empty(t, workflow.probes)
			require.Empty(t, environments.setKeys)
			require.Equal(t, before, environments.values["unrelated"])
			require.Empty(t, projects.added)
			require.Empty(t, projects.configValues)
			require.NoFileExists(t, "azure.yaml")
			require.NoFileExists(t, ".agentignore")
			actual, err := os.ReadFile(envPath)
			require.NoError(t, err)
			require.Equal(t, state, actual)
			if binding != nil {
				actual, err = os.ReadFile(configPath)
				require.NoError(t, err)
				require.Equal(t, binding, actual)
			}
			actual, err = os.ReadFile("main.py")
			require.NoError(t, err)
			require.Equal(t, source, actual)
		})
	}
}

func TestInitACRImageOnlyEffectiveSourceBuild(t *testing.T) {
	for _, authored := range []string{"none", "properties", "typed", "false-only"} {
		t.Run(authored, func(t *testing.T) {
			svc := agentServiceConfig(t, "agent", nil)
			svc.Image = "registry.example.com/agent:v1"
			if authored == "properties" {
				svc = agentServiceConfig(t, "agent", map[string]any{
					"docker": map[string]any{"remoteBuild": false, "path": "Dockerfile.custom", "context": "./src"},
				})
				svc.Image = "registry.example.com/agent:v1"
			}
			if authored == "typed" {
				svc.Docker = &azdext.DockerProjectOptions{Path: "Dockerfile.custom", Context: "./src"}
			}
			server := &deployModeProjectServer{path: t.TempDir(), services: map[string]*azdext.ServiceConfig{"agent": svc}}
			if authored == "false-only" {
				svc.Docker = &azdext.DockerProjectOptions{}
				content := "name: test\nservices:\n  agent:\n    host: azure.ai.agent\n" +
					"    kind: hosted\n    name: agent\n    language: docker\n" +
					"    image: registry.example.com/agent:v1\n    docker:\n      remoteBuild: false\n"
				require.NoError(t, os.WriteFile(filepath.Join(server.path, "azure.yaml"), []byte(content), 0o600))
				require.Nil(t, svc.AdditionalProperties)
				require.False(t, svc.Docker.RemoteBuild)
				svc.Language = "docker"
			}
			client := newProjectRecorderClient(t, server)
			needsACR, sources, err := applyDeployModeToAdoptedProjectWithSources(
				t.Context(), &initFlags{effectiveACRResourceID: testInitACRID}, client,
			)
			require.NoError(t, err)
			require.True(t, needsACR)
			sets := server.sets["agent"]
			require.Equal(t, ".", sets["project"])
			effective := map[string]any{"image": svc.Image}
			maps.Copy(effective, sets)
			if authored == "none" {
				require.Equal(t, []string{"agent"}, sources)
				require.Equal(t, "docker", sets["language"])
				require.Equal(t, map[string]any{"remoteBuild": true}, sets["docker"])
			} else {
				require.Empty(t, sources)
				require.NotContains(t, sets, "docker")
				require.NotContains(t, sets, "language")
			}
			if authored == "false-only" {
				require.NoError(t, finalizeAdoptedSourceContainerNetwork(t.Context(), client, sources, false))
				content, err := os.ReadFile(filepath.Join(server.path, "azure.yaml"))
				require.NoError(t, err)
				var document map[string]any
				require.NoError(t, yaml.Unmarshal(content, &document))
				services, ok := document["services"].(map[string]any)
				require.True(t, ok)
				agent, ok := services["agent"].(map[string]any)
				require.True(t, ok)
				require.Equal(t, map[string]any{"remoteBuild": false}, agent["docker"])
				directNeedsACR, err := applyDeployModeToService(t.Context(),
					&initFlags{acrResourceIDSet: true, acrResourceID: testInitACRID, deployMode: "container"},
					client, server.path, "agent", svc)
				require.NoError(t, err)
				require.True(t, directNeedsACR)
				require.NotContains(t, server.sets["agent"], "docker")
			}
			raw, err := yaml.Marshal(effective)
			require.NoError(t, err)
			var config struct {
				Project  string `yaml:"project"`
				Language string `yaml:"language"`
			}
			require.NoError(t, yaml.Unmarshal(raw, &config))
			skipsBuild := config.Project == "" && (config.Language == "" || config.Language == "docker")
			require.False(t, skipsBuild)
		})
	}
}

func TestInitACRImagePassthroughOnlyRejectedBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprint(saved), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n" +
				"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n" +
				"    image: registry.example.com/agent:v1\n    docker:\n      imagePassthrough: true\n")
			require.NoError(t, os.WriteFile("azure.yaml", content, 0o600))
			values := map[string]string{"AZURE_SUBSCRIPTION_ID": "sub"}
			if saved {
				values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testInitACRID
			}
			environments := &initACREnvironmentServer{testEnvironmentServiceServer: testEnvironmentServiceServer{
				values: map[string]map[string]string{"test": values},
			}}
			workflow := &recordingProjectWorkflowServer{}
			client := newTestAzdClient(t, environments, workflow)
			flags := &initFlags{acrResourceIDSet: !saved, acrResourceID: testInitACRID}
			require.ErrorContains(t, validateInitACRProject(t.Context(), client, flags),
				"without hosted-container source builds")
			require.Empty(t, environments.setKeys)
			require.Empty(t, workflow.requests)
			require.Empty(t, workflow.probes)
			actual, err := os.ReadFile("azure.yaml")
			require.NoError(t, err)
			require.Equal(t, content, actual)
		})
	}
}

func TestInitACRFalseOnlyDockerPreserved(t *testing.T) {
	for _, image := range []bool{false, true} {
		for _, mode := range []string{"", "container"} {
			for _, explicit := range []bool{false, true} {
				t.Run(fmt.Sprintf("image=%t/mode=%s/explicit=%t", image, mode, explicit), func(t *testing.T) {
					root := t.TempDir()
					content := "name: test\nservices:\n  agent:\n    host: azure.ai.agent\n" +
						"    kind: hosted\n    name: agent\n    language: docker\n    docker:\n      remoteBuild: false\n"
					svc := agentServiceConfig(t, "agent", map[string]any{"kind": "hosted", "name": "agent"})
					svc.Language = "docker"
					svc.Docker = &azdext.DockerProjectOptions{}
					if image {
						svc.Image = "registry.example.com/agent:v1"
						content += "    image: " + svc.Image + "\n"
					}
					path := filepath.Join(root, "azure.yaml")
					require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
					server := &deployModeProjectServer{path: root, services: map[string]*azdext.ServiceConfig{"agent": svc}}
					client := newProjectRecorderClient(t, server)
					flags := &initFlags{deployMode: mode}
					if explicit {
						flags.acrResourceIDSet, flags.acrResourceID = true, testInitACRID
					} else {
						flags.effectiveACRResourceID = testInitACRID
					}
					needsACR, sources, err := applyDeployModeToAdoptedProjectWithSources(t.Context(), flags, client)
					require.NoError(t, err)
					require.True(t, needsACR)
					require.Empty(t, sources)
					require.Equal(t, map[string]any{"project": "."}, server.sets["agent"])
					require.NoError(t, finalizeAdoptedSourceContainerNetwork(t.Context(), client, sources, false))
					require.Equal(t, map[string]any{"project": "."}, server.sets["agent"])
					actual, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, []byte(content), actual)
				})
			}
		}
	}
}

func TestInitACRInteractiveVoiceSavedSelectorBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, binding := range []bool{false, true} {
		t.Run(fmt.Sprint(binding), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n")
			require.NoError(t, os.WriteFile("azure.yaml", content, 0o600))
			environments := &initACREnvironmentServer{testEnvironmentServiceServer: testEnvironmentServiceServer{
				values: map[string]map[string]string{"test": {
					"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
				}},
			}}
			if binding {
				data, err := json.Marshal(synthesis.ACRBinding{
					Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
					ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
						"Microsoft.CognitiveServices/accounts/account/projects/project",
					PrincipalID: "principal",
				})
				require.NoError(t, err)
				environments.binding = data
				delete(environments.values["test"], "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
			}
			before := maps.Clone(environments.values["test"])
			workflow := &recordingProjectWorkflowServer{}
			client := newTestAzdClient(t, environments, workflow)
			flags := &initFlags{}
			require.NoError(t, validateInitACRProject(t.Context(), client, flags))
			require.Equal(t, testInitACRID, initACRResourceID(flags))
			err := runInitVoice(t.Context(), flags, client, ".", "", io.Discard)
			require.ErrorContains(t, err, initACRSelectorSource(flags)+" cannot be combined with interactive voice")
			require.Empty(t, workflow.requests)
			require.Empty(t, workflow.probes)
			require.Empty(t, environments.setKeys)
			require.Equal(t, before, environments.values["test"])
			actual, err := os.ReadFile("azure.yaml")
			require.NoError(t, err)
			require.Equal(t, content, actual)
			require.NoDirExists(t, ".azure")
		})
	}
}

func TestInitACRProjectsCapabilityBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, boundary := range []string{"supported", "unsupported", "check error", "no selector"} {
		t.Run(boundary, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.WriteFile("main.py", []byte("print('hello')\n"), 0o600))
			environments := &testEnvironmentServiceServer{}
			workflow := &recordingProjectWorkflowServer{err: errors.New("stopped at core init")}
			if boundary == "unsupported" || boundary == "no selector" {
				workflow.probeHelp = []byte("Flags:\n      --new-project\n")
			} else if boundary == "check error" {
				workflow.probeErr = status.Error(codes.Unavailable, "host unavailable")
			}
			projects := &recordingProjectServer{getProjectErr: status.Error(codes.NotFound, "no project")}
			client := newProjectWorkflowClient(t, projects, workflow)
			flags := &initFlags{noPrompt: true, src: ".", env: "test", protocols: []string{"responses"}}
			flags.projectsAddHelp = testProjectsACRHelp(workflow)
			if boundary != "no selector" {
				flags.acrResourceIDSet = true
				flags.acrResourceID = testInitACRID
			}
			action := &InitFromCodeAction{
				azdClient: client, flags: flags, sourceValidated: true, noticeWriter: io.Discard,
			}
			err := action.Run(t.Context())
			if boundary == "unsupported" || boundary == "check error" {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, exterrors.CodeIncompatibleProjectsExtension, localErr.Code)
				if boundary == "check error" {
					require.ErrorContains(t, err, workflow.probeErr.Error())
				} else {
					require.ErrorContains(t, err, "does not support")
				}
				require.Empty(t, workflow.requests)
				require.False(t, flags.acrCapabilityChecked)
			} else {
				require.ErrorContains(t, err, "stopped at core init")
				require.Len(t, workflow.requests, 1)
			}
			if boundary == "no selector" {
				require.Empty(t, workflow.probes)
			} else {
				require.Len(t, workflow.probes, 1)
				args := workflow.probes[0].Workflow.Steps[0].Command.Args
				require.Contains(t, args, "--help")
				require.Contains(t, args, "--no-prompt")
				require.NotContains(t, args, testInitACRID)
			}
			require.Empty(t, environments.setKeys)
			require.Empty(t, projects.added)
			require.Empty(t, projects.configValues)
			require.NoFileExists(t, "azure.yaml")
			require.NoDirExists(t, ".azure")
		})
	}
}

func TestInitACROwnershipSummary(t *testing.T) {
	for _, source := range []string{"explicit", "environment", "binding", "absent"} {
		for _, output := range []string{"", "table", "none", "json"} {
			for _, failed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/failed=%t", source, output, failed), func(t *testing.T) {
					flags := &initFlags{}
					switch source {
					case "explicit":
						flags.acrResourceIDSet, flags.acrResourceID = true, testInitACRID
						flags.effectiveACRResourceID = "ignored"
					case "environment", "binding":
						flags.effectiveACRResourceID = testInitACRID
					}
					var out, stderr bytes.Buffer
					cmd := &cobra.Command{}
					cmd.SetOut(&out)
					cmd.SetErr(&stderr)
					var runErr error
					if failed {
						runErr = errors.New("initialization failed")
					}
					printInitACROwnershipSummary(cmd, flags, output, runErr)
					if source == "absent" || failed || output == "none" || output == "json" {
						require.Empty(t, out.String())
					} else {
						require.Equal(t, "Source-build registry: "+testInitACRID+" (externally owned).\n", out.String())
					}
					require.Empty(t, stderr.String())
				})
			}
		}
	}
}

func TestInitACRProjectsCapabilityRejectsTemplateAndReuseBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, route := range []string{"template", "reuse"} {
		for _, selector := range []string{"explicit", "environment", "binding"} {
			t.Run(route+"/"+selector, func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				content := []byte("name: test\nservices:\n  foundry:\n    host: azure.ai.project\n" +
					"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n")
				values := map[string]string{"AZURE_SUBSCRIPTION_ID": "sub"}
				if selector == "environment" {
					values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = testInitACRID
				}
				before := maps.Clone(values)
				environments := &reuseInitACREnvironmentServer{testEnvironmentServiceServer: testEnvironmentServiceServer{
					current:      &azdext.Environment{Name: "test"},
					environments: map[string]*azdext.Environment{"test": {Name: "test"}},
					values:       map[string]map[string]string{"test": values},
					config:       map[string][]byte{},
				}}
				if selector == "binding" {
					binding, err := json.Marshal(synthesis.ACRBinding{
						Version: 1, Mode: "reuse-connect", RegistryResourceID: testInitACRID,
						ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry-rg/providers/" +
							"Microsoft.CognitiveServices/accounts/account/projects/project",
						PrincipalID: "principal",
					})
					require.NoError(t, err)
					environments.config[synthesis.ACRBindingConfigKey] = binding
				}
				beforeConfig := maps.Clone(environments.config)
				workflow := &recordingProjectWorkflowServer{probeErr: errors.New("unknown flag: --acr-resource-id")}
				var err error
				if route == "template" {
					template := t.TempDir()
					require.NoError(t, os.WriteFile(filepath.Join(template, "azure.yaml"), content, 0o600))
					dir := filepath.Join(root, ".azure", "test")
					require.NoError(t, os.MkdirAll(dir, 0o750))
					state := []byte("AZURE_SUBSCRIPTION_ID=sub\n")
					if selector == "environment" {
						state = append(state, []byte("AZURE_CONTAINER_REGISTRY_RESOURCE_ID="+testInitACRID+"\n")...)
					}
					envPath := filepath.Join(dir, ".env")
					require.NoError(t, os.WriteFile(envPath, state, 0o600))
					var binding []byte
					configPath := filepath.Join(dir, "config.json")
					if selector == "binding" {
						binding = writeInitACRRegressionBinding(t, configPath, testInitACRID)
					}
					flags := &initFlags{env: "test", templatePointer: template, noPrompt: true}
					flags.projectsAddHelp = testProjectsACRHelp(workflow)
					if selector == "explicit" {
						flags.acrResourceIDSet, flags.acrResourceID = true, testInitACRID
					}
					client := newTestAzdClient(t, environments, workflow)
					_, err = preflightAzdTemplate(t.Context(), client, http.DefaultClient, template, flags, ".", io.Discard)
					actual, readErr := os.ReadFile(envPath)
					require.NoError(t, readErr)
					require.Equal(t, state, actual)
					if binding != nil {
						actual, readErr = os.ReadFile(configPath)
						require.NoError(t, readErr)
						require.Equal(t, binding, actual)
					}
					require.NoFileExists(t, "azure.yaml")
				} else {
					require.NoError(t, os.WriteFile("azure.yaml", content, 0o600))
					project := &helpersProjectServer{project: &azdext.ProjectConfig{
						Path: root, Services: map[string]*azdext.ServiceConfig{
							"agent": inlineAgentService(t, "agent", "agent"),
						},
					}}
					var args []string
					if selector == "explicit" {
						args = []string{"--acr-resource-id", testInitACRID}
					}
					err = executeReuseInitACR(t, environments, workflow, &sourceInitACRPromptServer{}, project,
						&sourceInitACRAccountServer{}, true, "test", args...)
					actual, readErr := os.ReadFile("azure.yaml")
					require.NoError(t, readErr)
					require.Equal(t, content, actual)
					require.NoDirExists(t, ".azure")
				}
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "%v", err)
				require.Equal(t, exterrors.CodeIncompatibleProjectsExtension, localErr.Code)
				require.Len(t, workflow.probes, 1)
				require.Empty(t, workflow.requests)
				require.Empty(t, environments.setKeys)
				require.Equal(t, before, environments.values["test"])
				require.Equal(t, beforeConfig, environments.config)
			})
		}
	}
}

func TestInitACRWorkflowCallbackErrorCompletes(t *testing.T) {
	workflow := &recordingProjectWorkflowServer{runHook: func(int) error {
		return errors.New("unexpected environment creation arguments")
	}}
	client := newTestAzdClient(t, &testEnvironmentServiceServer{}, workflow)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := client.Workflow().Run(ctx, &azdext.RunWorkflowRequest{Workflow: &azdext.Workflow{
		Name: "test", Steps: []*azdext.WorkflowStep{{Command: &azdext.WorkflowCommand{
			Args: []string{"env", "new", "unexpected"},
		}}},
	}})
	require.ErrorContains(t, err, "unexpected environment creation arguments")
	require.NoError(t, ctx.Err())
}

func TestInitACRHelpProbeRejectsUnsupportedFlagsWithoutRun(t *testing.T) {
	ran := false
	cmd := &cobra.Command{Use: "add", RunE: func(*cobra.Command, []string) error {
		ran = true
		return nil
	}}
	cmd.Flags().Bool("new-project", false, "")
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--new-project", "--acr-resource-id", testInitACRID, "--help"})
	require.ErrorContains(t, cmd.Execute(), "unknown flag: --acr-resource-id")
	require.False(t, ran)
}

func TestInitACRProjectsHelpIsCaptured(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", bin)
	t.Setenv("PATH", bin)
	t.Setenv("NoDefaultCurrentDirectoryInExePath", "1")
	name := "projects"
	script := "#!/bin/sh\nprintf 'Flags:\\n      --new-project bool\\n      --acr-resource-id string\\n'\n"
	if runtime.GOOS == "windows" {
		name = "projects.cmd"
		script = "@echo Flags:\r\n@echo       --new-project bool\r\n@echo       --acr-resource-id string\r\n"
	}
	path := filepath.Join(bin, name)
	require.NoError(t, os.WriteFile(path, []byte(script), 0o600))
	//nolint:gosec // Only this test-owned helper is executable.
	require.NoError(t, os.Chmod(path, 0o700))
	config := &initProjectsCapabilityConfig{installed: map[string]map[string]string{
		"AZURE.AI.PROJECTS": {"id": "Azure.AI.Projects", "path": name, "version": "0.0.0"},
	}}
	client := newInstalledProjectsCapabilityClient(t, config, &recordingProjectServer{},
		&recordingProjectWorkflowServer{})
	help, err := projectsAddHelpFromCLI(t.Context(), client)
	require.NoError(t, err)
	require.Contains(t, string(help), "--acr-resource-id")
	flags := &initFlags{acrResourceIDSet: true, acrResourceID: testInitACRID}
	require.NoError(t, ensureInitACRProjectsCapability(t.Context(), flags, client))
	require.True(t, flags.acrCapabilityChecked)
	config.err = errors.New("must not probe twice")
	require.NoError(t, ensureInitACRProjectsCapability(t.Context(), flags, client))
}

type initProjectsCapabilityConfig struct {
	azdext.UnimplementedUserConfigServiceServer
	installed map[string]map[string]string
	err       error
}

func (s *initProjectsCapabilityConfig) Get(
	_ context.Context, req *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if req.Path != "extension.installed" {
		return nil, errors.New("unexpected user config lookup")
	}
	value, err := json.Marshal(s.installed)
	return &azdext.GetUserConfigResponse{Found: s.installed != nil, Value: value}, err
}

func newInstalledProjectsCapabilityClient(
	t *testing.T, config *initProjectsCapabilityConfig,
	projects *recordingProjectServer, workflow *recordingProjectWorkflowServer,
) *azdext.AzdClient {
	t.Helper()
	server := grpc.NewServer()
	azdext.RegisterUserConfigServiceServer(server, config)
	azdext.RegisterProjectServiceServer(server, projects)
	azdext.RegisterWorkflowServiceServer(server, workflow)
	azdext.RegisterEnvironmentServiceServer(server, &testEnvironmentServiceServer{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func TestInitACRInstalledProjectsCapabilityBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	for _, boundary := range []string{"absent", "lookup error", "missing executable", "unsupported", "similar flags"} {
		t.Run(boundary, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("AZD_CONFIG_DIR", root)
			t.Setenv("PATH", root)
			t.Setenv("NoDefaultCurrentDirectoryInExePath", "1")
			source := []byte("print('hello')\n")
			require.NoError(t, os.WriteFile("main.py", source, 0o600))
			name := "projects"
			azdName := "azd"
			help := "Flags:\\n      --new-project\\n"
			if boundary == "similar flags" {
				help = "Flags:\\n      --new-project-other\\n      --acr-resource-id-other string\\n"
			}
			script := "#!/bin/sh\nprintf '" + help + "'\n"
			decoy := "#!/bin/sh\nprintf 'Flags:\\n      --new-project\\n      --acr-resource-id string\\n'\n"
			if runtime.GOOS == "windows" {
				name, azdName = "projects.cmd", "azd.cmd"
				script = "@echo " + strings.ReplaceAll(help, "\\n", "\r\n@echo ") + "\r\n"
				decoy = "@echo Flags:\r\n@echo       --new-project\r\n@echo       --acr-resource-id string\r\n"
			}
			require.NoError(t, os.WriteFile(azdName, []byte(decoy), 0o600))
			//nolint:gosec // Only this test-owned helper is executable.
			require.NoError(t, os.Chmod(azdName, 0o700))
			config := &initProjectsCapabilityConfig{installed: map[string]map[string]string{
				"azure.ai.projects": {"id": "azure.ai.projects", "path": name, "version": "999.0.0"},
			}}
			switch boundary {
			case "absent":
				config.installed = nil
			case "lookup error":
				config.err = errors.New("host configuration unavailable")
			case "missing executable":
			default:
				require.NoError(t, os.WriteFile(name, []byte(script), 0o600))
				//nolint:gosec // Only this test-owned helper is executable.
				require.NoError(t, os.Chmod(name, 0o700))
			}
			projects := &recordingProjectServer{getProjectErr: status.Error(codes.NotFound, "no project")}
			workflow := &recordingProjectWorkflowServer{}
			client := newInstalledProjectsCapabilityClient(t, config, projects, workflow)
			flags := &initFlags{noPrompt: true, src: ".", env: "test", protocols: []string{"responses"},
				acrResourceIDSet: true, acrResourceID: testInitACRID}
			action := &InitFromCodeAction{
				azdClient: client, flags: flags, sourceValidated: true, noticeWriter: io.Discard,
			}
			err := action.Run(t.Context())
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "%v", err)
			require.Equal(t, exterrors.CodeIncompatibleProjectsExtension, localErr.Code)
			if boundary == "unsupported" || boundary == "similar flags" {
				require.ErrorContains(t, err, "does not support")
			} else {
				require.ErrorContains(t, err, "cannot verify")
			}
			require.False(t, flags.acrCapabilityChecked)
			require.Empty(t, workflow.requests)
			require.Empty(t, projects.added)
			require.Empty(t, projects.configValues)
			require.NoFileExists(t, "azure.yaml")
			require.NoFileExists(t, ".agentignore")
			require.NoDirExists(t, ".azure")
			actual, err := os.ReadFile("main.py")
			require.NoError(t, err)
			require.Equal(t, source, actual)
		})
	}
}

func TestInitACRProjectsCapabilityPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	flags := &initFlags{
		acrResourceIDSet: true, acrResourceID: testInitACRID,
		projectsAddHelp: func(context.Context) ([]byte, error) {
			cancel()
			return nil, errors.New("process stopped")
		},
	}
	defer cancel()
	require.ErrorIs(t, ensureInitACRProjectsCapability(ctx, flags, nil), context.Canceled)
	require.False(t, flags.acrCapabilityChecked)
}
