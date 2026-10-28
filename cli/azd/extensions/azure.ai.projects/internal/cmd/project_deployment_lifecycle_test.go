// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/internal/provisioning"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type adoptedDeploymentPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	choice int32
}

func (s *adoptedDeploymentPromptServer) Select(
	context.Context, *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	return &azdext.SelectResponse{Value: new(s.choice)}, nil
}

func TestAdoptedDeploymentChoicesPersistEffectiveList(t *testing.T) {
	yamlDeployment := synthesis.Deployment{
		Name: "yaml", Model: synthesis.DeploymentModel{Name: "model-a", Format: "OpenAI", Version: "v1"},
		Sku: synthesis.DeploymentSku{Name: "Standard", Capacity: 1},
	}
	selected := synthesis.Deployment{
		Name: "env", Model: synthesis.DeploymentModel{Name: "model-b", Format: "OpenAI", Version: "v2"},
		Sku: synthesis.DeploymentSku{Name: "Standard", Capacity: 7},
	}
	extra := selected
	extra.Name, extra.Model.Name = "extra", "other"
	for _, test := range []struct {
		name     string
		choice   int32
		noPrompt bool
		override bool
		extra    bool
		failKey  string
	}{
		{name: "reuse retains environment-only entry", override: true, extra: true},
		{name: "skip all persists empty", override: true, choice: 3},
		{name: "deploy preserves environment tuple", override: true, choice: 1},
		{name: "no prompt reuses existing", override: true, noPrompt: true},
		{name: "YAML fallback reuse preserves portable declarations"},
		{name: "YAML fallback skip preserves portable declarations", choice: 3},
		{name: "YAML fallback deploy seeds environment", choice: 1},
		{name: "YAML fallback no prompt preserves portable declarations", noPrompt: true},
		{name: "YAML fallback deployment write rollback", failKey: projectDeploymentsEnvKey},
		{name: "YAML fallback deploy write rollback", choice: 1, failKey: projectDeploymentsEnvKey},
		{name: "YAML fallback default write rollback", failKey: "AZURE_AI_MODEL_DEPLOYMENT_NAME"},
		{name: "deployment write rollback", override: true, failKey: projectDeploymentsEnvKey},
		{name: "default write rollback", override: true, failKey: "AZURE_AI_MODEL_DEPLOYMENT_NAME"},
		{name: "bindings write rollback", override: true, failKey: synthesis.DeploymentBindingsEnvKey},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, project, env, _, _ := newSelfInitializingDeploymentClient(
				t, t.TempDir(), &adoptedDeploymentPromptServer{choice: test.choice},
			)
			service := mustProjectProperties(t, map[string]any{
				"deployments": []any{deploymentMap(yamlDeployment)},
			}).AsMap()
			project.services["project"] = maps.Clone(service)
			effective := []synthesis.Deployment{yamlDeployment}
			if test.override {
				effective = []synthesis.Deployment{selected}
				if test.extra {
					effective = append(effective, extra)
				}
				encoded, err := provisioning.EncodeProjectDeployments(effective)
				require.NoError(t, err)
				env.values[projectDeploymentsEnvKey] = encoded
			}
			env.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"] = "old-default"
			original := maps.Clone(env.values)
			env.failKey = test.failKey
			live := []liveProjectDeployment{{Name: "existing", Model: effective[0].Model}}
			defaultName, changed, restore, err := reconcileAdoptedDeploymentChoices(
				t.Context(), client, "test", nil, []synthesis.Deployment{yamlDeployment},
				live, original, test.noPrompt,
			)
			assert.Equal(t, service, project.services["project"])
			assert.False(t, project.deploymentSet, "adoption must never write portable deployments")
			if test.failKey != "" {
				require.ErrorContains(t, err, "environment write failed")
				for key, value := range env.values {
					assert.Equal(t, original[key], value, "restore %s", key)
				}
				return
			}
			require.NoError(t, err)
			// Deploy-as-configured is a no-op only when an override already exists.
			wantChanged := !(test.override && test.choice == 1)
			assert.Equal(t, wantChanged, changed)
			got, present, err := provisioning.DecodeProjectDeployments(env.values[projectDeploymentsEnvKey])
			require.NoError(t, err)
			require.True(t, present)
			want := []synthesis.Deployment{}
			if test.choice == 1 {
				want = effective
			} else if test.extra {
				want = []synthesis.Deployment{extra}
			}
			assert.Equal(t, want, got)
			if test.choice == 3 {
				assert.Empty(t, defaultName)
				assert.Empty(t, env.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
			} else if test.choice != 1 {
				assert.Equal(t, "existing", defaultName)
			}
			require.NoError(t, restore())
			assert.Equal(t, original[projectDeploymentsEnvKey], env.values[projectDeploymentsEnvKey])
			assert.Equal(t, "old-default", env.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
			assert.Equal(t, service, project.services["project"])
			assert.False(t, project.deploymentSet, "rollback must never write portable deployments")
		})
	}
}

func TestEjectProjectInfraPreservesEffectiveDeployments(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	const resourceID = "/subscriptions/subscription/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	envDeployment := synthesis.Deployment{
		Name: "env-b", Model: synthesis.DeploymentModel{Name: "model-b", Format: "OpenAI", Version: "v2"},
		Sku: synthesis.DeploymentSku{Name: "Standard", Capacity: 7},
	}
	encoded, err := provisioning.EncodeProjectDeployments([]synthesis.Deployment{envDeployment})
	require.NoError(t, err)
	for _, existing := range []bool{false, true} {
		for _, provider := range []string{"bicep", "terraform"} {
			for _, value := range []string{encoded, "[]", "", "  ", "{"} {
				name := provider + "/new/" + value
				if existing {
					name = provider + "/existing/" + value
				}
				t.Run(name, func(t *testing.T) {
					root := t.TempDir()
					raw := "name: test\nservices:\n  project:\n    host: azure.ai.project\n"
					if existing {
						raw += "    endpoint: " + endpoint + "\n"
					}
					raw += "    deployments:\n      - name: yaml-a\n" +
						"        model: {name: model-a, format: OpenAI, version: v1}\n" +
						"        sku: {name: GlobalStandard, capacity: 1}\n"
					require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(raw), 0600))
					client, _, _, _, _ := newSelfInitializingDeploymentClient(t, root)
					env := map[string]string{projectDeploymentsEnvKey: value}
					targetEndpoint, targetID := "", ""
					if existing {
						targetEndpoint, targetID = endpoint, resourceID
					}
					err := ejectProjectInfraWithTarget(
						t.Context(), client, root, "project", provider, targetEndpoint, targetID, env,
					)
					if value == "{" {
						require.ErrorContains(t, err, projectDeploymentsEnvKey)
						assert.NoDirExists(t, filepath.Join(root, "infra"))
						return
					}
					require.NoError(t, err)
					file := "main.parameters.json"
					if provider == "terraform" {
						file = "main.tfvars.json"
					}
					// #nosec G304 -- test-owned path.
					data, err := os.ReadFile(filepath.Join(root, "infra", file))
					require.NoError(t, err)
					var output map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(data, &output))
					deployments := output["deployments"]
					if provider == "bicep" {
						var parameters map[string]struct {
							Value json.RawMessage `json:"value"`
						}
						require.NoError(t, json.Unmarshal(output["parameters"], &parameters))
						deployments = parameters["deployments"].Value
					}
					var got []synthesis.Deployment
					require.NoError(t, json.Unmarshal(deployments, &got))
					switch value {
					case encoded:
						assert.Equal(t, []synthesis.Deployment{envDeployment}, got)
					case "[]":
						assert.Equal(t, []synthesis.Deployment{}, got)
					default:
						require.Len(t, got, 1)
						assert.Equal(t, "yaml-a", got[0].Name)
					}
					userFile := filepath.Join(root, "infra", file)
					const userContent = "{\"deployments\": []}\n"
					require.NoError(t, os.WriteFile(userFile, []byte(userContent), 0600))
					require.Error(t, ejectProjectInfraWithTarget(
						t.Context(), client, root, "project", provider, targetEndpoint, targetID, env,
					))
					// #nosec G304 -- test-owned path.
					after, err := os.ReadFile(userFile)
					require.NoError(t, err)
					assert.Equal(t, userContent, string(after))
				})
			}
		}
	}
}

func TestAdoptedDeploymentsValidateOverrideBeforeAzure(t *testing.T) {
	for _, value := range []string{"[]", "{"} {
		t.Run(value, func(t *testing.T) {
			root := t.TempDir()
			client, project, env, _, _ := newSelfInitializingDeploymentClient(t, root)
			project.services["project"] = map[string]any{
				"host":     aiProjectHost,
				"endpoint": "https://account.services.ai.azure.com/api/projects/project",
				"deployments": []any{map[string]any{
					"name": "yaml-a", "model": map[string]any{"name": "model-a"},
				}},
			}
			env.values[projectDeploymentsEnvKey] = value
			original := maps.Clone(env.values)
			_, changed, _, err := reconcileAdoptedDeployments(
				t.Context(), client, root, "test",
				&resolvedProject{Mode: projectModeExistingID, ResourceId: "unused"},
				"project", true,
			)
			if value == "{" {
				require.ErrorContains(t, err, projectDeploymentsEnvKey)
			} else {
				require.NoError(t, err)
			}
			assert.False(t, changed)
			assert.Equal(t, original, env.values)
		})
	}
}
