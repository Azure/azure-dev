// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func promptModelServices(t *testing.T, deploymentNames ...string) (
	*azdext.ServiceConfig, map[string]*azdext.ServiceConfig,
) {
	t.Helper()
	deployments := make([]any, 0, len(deploymentNames))
	for _, name := range deploymentNames {
		deployments = append(deployments, map[string]any{
			"name":  name,
			"model": map[string]any{"name": "gpt-4.1-nano", "format": "OpenAI"},
		})
	}
	props, err := structpb.NewStruct(map[string]any{"deployments": deployments})
	require.NoError(t, err)
	agent := &azdext.ServiceConfig{Name: "agent", Host: "azure.ai.agent", Uses: []string{"ai-project"}}
	return agent, map[string]*azdext.ServiceConfig{
		"agent":      agent,
		"ai-project": {Name: "ai-project", Host: FoundryProjectHost, AdditionalProperties: props},
	}
}

func TestResolvePromptAgentModel(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		declared    []string
		env         map[string]string
		want        string
		wantErrText string
	}{
		{
			name:        "invalid environment list fails",
			model:       "template-chat",
			env:         map[string]string{"AI_PROJECT_DEPLOYMENTS": "{"},
			wantErrText: "decoding AI_PROJECT_DEPLOYMENTS",
		},
		{
			name:  "blank environment list is missing",
			model: "template-chat",
			env:   map[string]string{"AI_PROJECT_DEPLOYMENTS": "  "},
			want:  "template-chat",
		},
		{
			name:     "declaration references expand before comparison",
			model:    "${CHAT}",
			declared: []string{"${CHAT}", "template-chat"},
			env: map[string]string{
				"CHAT":                           "template-chat",
				"AI_PROJECT_DEPLOYMENTS":         "[]",
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			want: "existing-41",
		},
		{
			name:     "reused deployment falls back to environment default",
			model:    "template-chat",
			declared: []string{"template-chat"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         "[]",
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			want: "existing-41",
		},
		{
			name:     "escaped environment list is decoded",
			model:    "template-chat",
			declared: []string{"template-chat"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         `[{\"name\":\"other\"}]`,
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			want: "existing-41",
		},
		{
			name:     "one of several declared deployments replaced falls back",
			model:    "template-chat",
			declared: []string{"template-chat", "template-embed"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         `[{"name":"template-embed"}]`,
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			want: "existing-41",
		},
		{
			name:     "several missing declared deployments are ambiguous",
			model:    "template-chat",
			declared: []string{"template-chat", "template-embed"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         "[]",
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			wantErrText: "cannot be inferred",
		},
		{
			name:        "skipped deployment without default",
			model:       "template-chat",
			declared:    []string{"template-chat"},
			env:         map[string]string{"AI_PROJECT_DEPLOYMENTS": "[]"},
			wantErrText: "was skipped for this environment",
		},
		{
			name:     "deployment created in this environment is unchanged",
			model:    "template-chat",
			declared: []string{"template-chat"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         `[{"name":"template-chat"}]`,
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			want: "template-chat",
		},
		{
			name:     "deployment selection membership is case-insensitive",
			model:    "Chat",
			declared: []string{"chat"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         `[{"name":"chat"}]`,
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			want: "Chat",
		},
		{
			name:     "unset environment list is unchanged",
			model:    "template-chat",
			declared: []string{"template-chat"},
			env:      map[string]string{"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41"},
			want:     "template-chat",
		},
		{
			name:     "declared membership is case-insensitive",
			model:    "chat",
			declared: []string{"Chat"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         "[]",
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			want: "existing-41",
		},
		{
			name:     "undeclared model is unchanged",
			model:    "gpt-4.1-mini",
			declared: []string{"template-chat"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         "[]",
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			want: "gpt-4.1-mini",
		},
		{
			name:     "environment reference is expanded",
			model:    "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
			declared: []string{"template-chat"},
			env:      map[string]string{"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41"},
			want:     "existing-41",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, services := promptModelServices(t, tt.declared...)
			got, err := ResolvePromptAgentModel(tt.model, agent, services, t.TempDir(), tt.env)
			if tt.wantErrText != "" {
				require.ErrorContains(t, err, tt.wantErrText)
				if tt.wantErrText == "decoding AI_PROJECT_DEPLOYMENTS" {
					return
				}
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, exterrors.CodeModelDeploymentNotFound, localErr.Code)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestResolvePromptAgentModelExpandsReferencedDeclarations(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.yaml"), []byte(
		"deployments:\n  - name: ${CHAT}\n",
	), 0600))
	agent, services := promptModelServices(t)
	props, err := structpb.NewStruct(map[string]any{"$ref": "./project.yaml"})
	require.NoError(t, err)
	services["ai-project"].AdditionalProperties = props
	t.Setenv("CHAT", "process-chat")
	got, err := ResolvePromptAgentModel("${CHAT}", agent, services, root, map[string]string{
		"CHAT":                           "template-chat",
		"AI_PROJECT_DEPLOYMENTS":         "[]",
		"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing",
	})
	require.NoError(t, err)
	require.Equal(t, "existing", got)
}

func TestResolvePromptAgentModelWithOnDiskInfrastructure(t *testing.T) {
	for _, test := range []struct {
		name   string
		config string
		path   string
		files  map[string]string
		onDisk bool
	}{
		{
			name: "root Bicep", path: "infra", onDisk: true,
			files: map[string]string{
				"main.bicep": "",
				"main.parameters.json": `{"parameters":{"deployments":` +
					`{"value":[{"name":"chat"},{"name":"embeddings"}]}}}`,
			},
		},
		{
			name: "custom Bicep parameters", path: "custom",
			config: "infra:\n  path: custom\n  module: models\n", onDisk: true,
			files: map[string]string{"models.bicepparam": ""},
		},
		{
			name: "root Terraform", path: "infra",
			config: "infra:\n  provider: terraform\n", onDisk: true,
			files: map[string]string{"main.tf": ""},
		},
		{
			name: "Foundry layer", path: "infra/foundry",
			config: "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n" +
				"      path: infra/foundry\n      module: models\n",
			files: map[string]string{"models.bicep": ""}, onDisk: true,
		},
		{
			name: "legacy Terraform layer", path: "infra/foundry",
			config: "infra:\n  layers:\n    - name: foundry\n      provider: terraform\n" +
				"      path: infra/foundry\n",
			files: map[string]string{
				"main.tf": "resource \"azapi_resource\" \"foundry_account\" {}\n" +
					"resource \"azapi_resource\" \"project\" {}\n",
				"main.tfvars.json": `{"deployments":[{"name":"chat"},{"name":"embeddings"}]}`,
			}, onDisk: true,
		},
		{
			name: "ejected Terraform layer", path: "infra/foundry",
			config: "infra:\n  layers:\n    - name: foundry\n      provider: terraform\n" +
				"      path: infra/foundry\n",
			files: map[string]string{
				".azd-foundry": "terraform-v1\n", "main.tf": "",
			}, onDisk: true,
		},
		{
			name: "unrelated Bicep layer", path: "infra/app",
			config: "infra:\n  layers:\n    - name: app\n      provider: bicep\n      path: infra/app\n",
			files:  map[string]string{"main.bicep": ""},
		},
		{
			name: "unrelated Terraform layer", path: "infra/app",
			config: "infra:\n  layers:\n    - name: app\n      provider: terraform\n      path: infra/app\n",
			files:  map[string]string{"main.tf": ""},
		},
		{
			name: "embedded Foundry", path: "infra",
			config: "infra:\n  provider: microsoft.foundry\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(test.config), 0600))
			dir := filepath.Join(root, filepath.FromSlash(test.path))
			require.NoError(t, os.MkdirAll(dir, 0700))
			for name, content := range test.files {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0600))
			}
			for _, declarations := range [][]string{{"chat"}, {"chat", "embeddings"}} {
				agent, services := promptModelServices(t, declarations...)
				env := map[string]string{
					"AI_PROJECT_DEPLOYMENTS":         "[]",
					"AZURE_AI_MODEL_DEPLOYMENT_NAME": "unrelated-default",
					"CHAT":                           "chat",
				}
				got, err := ResolvePromptAgentModel("${CHAT}", agent, services, root, env)
				if test.onDisk {
					require.NoError(t, err)
					require.Equal(t, "chat", got)
					got, err = ResolvePromptAgentModel(
						"${AZURE_AI_MODEL_DEPLOYMENT_NAME}", agent, services, root, env,
					)
					require.NoError(t, err)
					require.Equal(t, "unrelated-default", got)
				} else if len(declarations) == 1 {
					require.NoError(t, err)
					require.Equal(t, "unrelated-default", got)
				} else {
					require.ErrorContains(t, err, "cannot be inferred")
				}
			}

		})
	}
}

func TestResolvePromptAgentModelInfrastructureErrors(t *testing.T) {
	for _, directory := range []bool{false, true} {
		root := t.TempDir()
		path := filepath.Join(root, "azure.yaml")
		if directory {
			require.NoError(t, os.Mkdir(path, 0700))
		} else {
			require.NoError(t, os.WriteFile(path, []byte("infra: ["), 0600))
		}

		agent, services := promptModelServices(t, "chat")
		_, err := ResolvePromptAgentModel("chat", agent, services, root, map[string]string{
			"AI_PROJECT_DEPLOYMENTS": "[]",
		})
		require.ErrorContains(t, err, "infrastructure configuration")
	}
}

func TestResolvePromptAgentModelExplicitBindings(t *testing.T) {
	for _, onDisk := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "ejected"}[onDisk], func(t *testing.T) {
			root := t.TempDir()
			if onDisk {
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
					"infra:\n  provider: terraform\n",
				), 0600))
			}
			agent, services := promptModelServices(t, "${CHAT}", "embeddings", "skipped")
			env := map[string]string{
				"CHAT": "chat", "AI_PROJECT_DEPLOYMENTS": "[]",
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "unrelated-default",
				"AI_PROJECT_DEPLOYMENT_BINDINGS": `{\"chat\":\"selected-chat\",` +
					`\"embeddings\":\"selected-embeddings\",\"skipped\":\"\",\"undeclared\":\"wrong\"}`,
			}
			for _, test := range []struct{ model, want string }{
				{"${CHAT}", "selected-chat"},
				{"embeddings", "selected-embeddings"},
				{"undeclared", "undeclared"},
				{"${AZURE_AI_MODEL_DEPLOYMENT_NAME}", "unrelated-default"},
			} {
				got, err := ResolvePromptAgentModel(test.model, agent, services, root, env)
				require.NoError(t, err)
				require.Equal(t, test.want, got)
			}
			_, err := ResolvePromptAgentModel("skipped", agent, services, root, env)
			require.ErrorContains(t, err, "was skipped")
			env["AI_PROJECT_DEPLOYMENT_BINDINGS"] = "{"
			_, err = ResolvePromptAgentModel("chat", agent, services, root, env)
			require.ErrorContains(t, err, "AI_PROJECT_DEPLOYMENT_BINDINGS")
		})
	}
}

func TestResolvePromptAgentModelExplicitBindingsCaseInsensitive(t *testing.T) {
	agent, services := promptModelServices(t, "MyDeployment", "Chat", "Skipped")
	env := map[string]string{
		"AI_PROJECT_DEPLOYMENTS":         "[]",
		"AZURE_AI_MODEL_DEPLOYMENT_NAME": "unrelated-default",
		"AI_PROJECT_DEPLOYMENT_BINDINGS": `{"mydeployment":"selected-deployment","CHAT":"selected-chat",` +
			`"sKiPpEd":"","UnDeclared":"wrong"}`,
	}

	root := t.TempDir()
	got, err := ResolvePromptAgentModel("MyDeployment", agent, services, root, env)
	require.NoError(t, err)
	require.Equal(t, "selected-deployment", got)

	got, err = ResolvePromptAgentModel("chat", agent, services, root, env)
	require.NoError(t, err)
	require.Equal(t, "selected-chat", got)

	_, err = ResolvePromptAgentModel("skipped", agent, services, root, env)
	require.ErrorContains(t, err, "was skipped")

	got, err = ResolvePromptAgentModel("uNdEcLaReD", agent, services, root, env)
	require.NoError(t, err)
	require.Equal(t, "uNdEcLaReD", got)
}

func TestResolvePromptAgentModelExplicitBindingsUnsafeInfrastructure(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
		"infra:\n  path: ../outside\n",
	), 0600))
	agent, services := promptModelServices(t, "chat")
	for _, replacement := range []string{"selected-chat", ""} {
		t.Run("replacement="+replacement, func(t *testing.T) {
			got, err := ResolvePromptAgentModel("chat", agent, services, root, map[string]string{
				"AI_PROJECT_DEPLOYMENT_BINDINGS": `{"chat":"` + replacement + `"}`,
			})
			require.ErrorContains(t, err, "resolve azure.yaml infra.path")
			require.ErrorContains(t, err, "must not contain '..'")
			require.Empty(t, got)
		})
	}
}
