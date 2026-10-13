// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

func mustStruct(t *testing.T, fields map[string]any) *structpb.Struct {
	t.Helper()

	s, err := structpb.NewStruct(fields)
	require.NoError(t, err)
	return s
}

// TestDeclaredAgentDefinitionRef covers where the `$ref:` include may live on a
// service entry. Service-level properties win over the nested config block so
// the unified azure.yaml shape reads the same way the inline agent definition
// does.
func TestDeclaredAgentDefinitionRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		svc  *azdext.ServiceConfig
		want string
	}{
		{
			name: "nil service",
		},
		{
			name: "no ref declared falls back to the convention",
			svc:  &azdext.ServiceConfig{Name: "agent"},
		},
		{
			name: "service-level ref",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"$ref": "./agent.yaml"}),
			},
			want: "./agent.yaml",
		},
		{
			name: "config-level ref is not a runtime source",
			svc: &azdext.ServiceConfig{
				Config: mustStruct(t, map[string]any{"$ref": "./nested.yaml"}),
			},
		},
		{
			name: "service-level wins over config-level",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"$ref": "./outer.yaml"}),
				Config:               mustStruct(t, map[string]any{"$ref": "./inner.yaml"}),
			},
			want: "./outer.yaml",
		},
		{
			name: "blank value is treated as undeclared",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"$ref": "   "}),
			},
		},
		{
			name: "non-string value is ignored",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"$ref": 42}),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, declaredAgentDefinitionRef(tc.svc))
		})
	}
}

// TestResolveDeclaredRefPath pins the confinement rules. A `$ref` resolves
// against the directory holding azure.yaml — the same anchor the shared include
// machinery uses — and may not escape it.
func TestResolveDeclaredRefPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	tests := []struct {
		name     string
		declared string
		wantRel  string
		wantErr  bool
	}{
		{
			name:     "sibling file",
			declared: "./agent.yaml",
			wantRel:  "agent.yaml",
		},
		{
			name:     "bare name",
			declared: "agent.yaml",
			wantRel:  "agent.yaml",
		},
		{
			name:     "nested file",
			declared: "./src/triage/agent.yml",
			wantRel:  filepath.Join("src", "triage", "agent.yml"),
		},
		{
			name:     "escaping the project root is rejected",
			declared: "../agent.yaml",
			wantErr:  true,
		},
		{
			name:     "absolute paths are rejected",
			declared: "/etc/agent.yaml",
			wantErr:  true,
		},
		{
			name:     "JSON file",
			declared: "./agent.json",
			wantRel:  "agent.json",
		},
		{
			name:     "uppercase JSON extension",
			declared: "./definitions/agent.JSON",
			wantRel:  filepath.Join("definitions", "agent.JSON"),
		},
		{
			name:     "escaping JSON path is rejected",
			declared: "../agent.json",
			wantErr:  true,
		},
		{
			name:     "absolute JSON path is rejected",
			declared: filepath.Join(root, "agent.json"),
			wantErr:  true,
		},
		{
			name:     "unsupported extensions are rejected",
			declared: "./agent.txt",
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolveDeclaredRefPath(root, tc.declared, "triage-agent")
			if tc.wantErr {
				require.Error(t, err)
				if tc.declared == "./agent.txt" {
					localErr, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					require.Contains(t, localErr.Message, "YAML or JSON")
					require.Equal(t, "point $ref: at a .yaml, .yml, or .json file", localErr.Suggestion)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, filepath.Join(root, tc.wantRel), got)
		})
	}
}

func TestAgentRootRefRuntimeLoaders(t *testing.T) {
	for _, kind := range []string{
		"prompt", "hosted", "hosted-code", "voice", "prompt-voice",
	} {
		for _, extension := range []string{".yaml", ".yml", ".json", ".JSON"} {
			t.Run(kind+extension, func(t *testing.T) {
				root := t.TempDir()
				definitionDir := filepath.Join(root, "definitions")
				serviceDir := filepath.Join(root, "src", "agent")
				require.NoError(t, os.MkdirAll(definitionDir, 0o750))
				require.NoError(t, os.MkdirAll(serviceDir, 0o750))
				require.NoError(t, os.WriteFile(
					filepath.Join(definitionDir, "instructions.md"),
					[]byte("Follow these instructions."),
					0o600,
				))

				values := map[string]any{
					"kind": kind,
					"name": "referenced-name",
				}
				switch kind {
				case "prompt":
					values["model"] = "gpt-4.1-mini"
					values["instructions"] = "./instructions.md"
					values["harness"] = map[string]any{"$ref": "./harness.json"}
					require.NoError(t, os.WriteFile(
						filepath.Join(definitionDir, "harness.json"),
						[]byte(`{"type":"github_copilot_preview"}`),
						0o600,
					))
				case "hosted-code":
					values["kind"] = "hosted"
					values["codeConfiguration"] = map[string]any{
						"runtime": "python_3_13", "entryPoint": "app.py",
					}
					require.NoError(t, os.WriteFile(
						filepath.Join(serviceDir, "app.py"),
						[]byte("print('service source')\n"),
						0o600,
					))
				case "voice", "prompt-voice":
					values["model"] = map[string]any{"id": "gpt-realtime"}
					values["instructions"] = "./instructions.md"
				}
				data, err := json.Marshal(values)
				if extension == ".yaml" || extension == ".yml" {
					data, err = yaml.Marshal(values)
				}
				require.NoError(t, err)
				ref := "./definitions/agent" + extension
				definitionPath := filepath.Join(definitionDir, "agent"+extension)
				require.NoError(t, os.WriteFile(definitionPath, data, 0o600))
				svc := &azdext.ServiceConfig{
					Name:         "agent-service",
					Host:         "azure.ai.agent",
					RelativePath: "src/agent",
					AdditionalProperties: mustStruct(t, map[string]any{
						"$ref": ref, "name": "overlay-name",
					}),
				}
				if kind == "hosted" {
					svc.Image = "registry.example/agent:v1"
				}

				validation, err := ValidateAgentServiceDefinition(svc, root)
				require.NoError(t, err)
				require.Equal(t, "overlay-name", validation.Name)
				provider := &AgentServiceTargetProvider{
					azdClient: newInitializeTestClient(t, root),
				}
				require.NoError(t, provider.Initialize(t.Context(), svc))
				require.Equal(t, ref, provider.agentDefinitionRef)
				require.NoError(t, provider.ensureDeployContext(t.Context()))
				require.Equal(t, serviceDir, provider.servicePath)
				require.True(t, provider.deployContextReady)
				require.NotContains(t, svc.AdditionalProperties.Fields, "$ref")
				require.Equal(t, "agent-service", svc.Name)
				require.Equal(t, "src/agent", svc.RelativePath)

				switch kind {
				case "prompt":
					require.Equal(t, definitionPath, provider.agentDefinitionPath)
					require.Equal(t, definitionDir, provider.promptAgentConventionDir())
					managed, err := provider.loadPromptAgentDefinition()
					require.NoError(t, err)
					require.Equal(t, "overlay-name", managed.Name)
					require.Equal(t, "gpt-4.1-mini", managed.Model)
					require.Equal(t, "definitions/instructions.md", managed.Instructions)
					require.Equal(t, "github_copilot_preview", managed.Harness.Type)
					validationDir, err := promptAgentValidationDir(
						provider.agentDefinitionValidationServiceConfig(), root,
					)
					require.NoError(t, err)
					require.Equal(t, definitionDir, validationDir)
				case "hosted", "hosted-code":
					hosted, found, err := provider.loadContainerAgentDefinition()
					require.NoError(t, err)
					require.True(t, found)
					require.Equal(t, "overlay-name", hosted.Name)
					require.Empty(t, provider.agentDefinitionPath)
					if kind == "hosted" {
						require.Equal(t, svc.Image, hosted.Image)
						break
					}
					require.NotNil(t, hosted.CodeConfiguration)
					require.Equal(t, "app.py", hosted.CodeConfiguration.EntryPoint)
					result, err := provider.Package(
						t.Context(), svc, &azdext.ServiceContext{}, func(string) {},
					)
					require.NoError(t, err)
					require.Len(t, result.Artifacts, 1)
					artifact := result.Artifacts[0]
					t.Cleanup(func() { require.NoError(t, os.Remove(artifact.Location)) })
					require.Equal(t, "code-zip", artifact.Metadata["type"])
					require.NotEmpty(t, artifact.Metadata["sha256"])
					archive, err := zip.OpenReader(artifact.Location)
					require.NoError(t, err)
					require.Len(t, archive.File, 1)
					require.Equal(t, "app.py", archive.File[0].Name)
					require.NoError(t, archive.Close())
				case "voice", "prompt-voice":
					voice, found, err := resolveVoiceAgentForDeploy(svc, root)
					require.NoError(t, err)
					require.True(t, found)
					require.Equal(t, agent_yaml.AgentKind(kind), voice.Kind)
					require.Equal(t, "overlay-name", voice.Name)
					require.Equal(t, "gpt-realtime", voice.Model.Id)
					require.Equal(t, new("definitions/instructions.md"), voice.Instructions)
				}
				unchanged, err := os.ReadFile(definitionPath)
				require.NoError(t, err)
				require.Equal(t, data, unchanged)
			})
		}
	}
}

func TestPromptJSONRootRefRejectsInvalidFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "missing"},
		{name: "malformed", content: `{"kind":`},
		{name: "non-object", content: `["prompt"]`},
		{name: "remote", content: `{"$ref":"https://example.com/agent.json"}`},
		{name: "cycle", content: `{"$ref":"./agent.json"}`},
		{name: "core field", content: `{"kind":"prompt","project":"./src"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.content != "" {
				require.NoError(t, os.WriteFile(
					filepath.Join(root, "agent.json"), []byte(tt.content), 0o600,
				))
			}
			provider := &AgentServiceTargetProvider{
				projectPath: root,
				serviceConfig: promptService(t, map[string]any{
					"$ref": "./agent.json",
				}),
			}
			require.Error(t, provider.Initialize(t.Context(), provider.serviceConfig))
			_, err := provider.loadPromptAgentDefinition()
			require.Error(t, err)
			require.False(t, provider.deployContextReady)
			require.Empty(t, provider.agentDefinitionPath)
			require.Equal(t, "./agent.json", declaredAgentDefinitionRef(provider.serviceConfig))
			if tt.name == "missing" {
				require.ErrorContains(t, err, "agent.json")
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, foundry.CodeInvalidFileRef, localErr.Code)
			}
		})
	}
}
