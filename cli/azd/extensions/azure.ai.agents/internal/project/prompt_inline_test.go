// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/require"
)

// TestPromptAgentInlineRoundTripPreservesMemory is the regression test for the
// reason PromptAgentInline exists.
//
// agent_yaml.PromptAgent tags Memory json:"-" because the prompt-agent API
// defines no such field. Service properties round-trip through JSON, so
// marshaling a PromptAgent directly would drop an authored memory block with no
// error and no diagnostic — the agent would simply deploy without recall.
func TestPromptAgentInlineRoundTripPreservesMemory(t *testing.T) {
	t.Parallel()

	original := agent_yaml.PromptAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindPrompt,
			Name: "memory-agent",
		},
		Model:        "gpt-4.1-mini",
		Instructions: "You are a helpful AI assistant.",
		Memory: &agent_yaml.PromptMemory{
			Store:          "conversation-store",
			ChatModel:      "gpt-4.1-mini",
			EmbeddingModel: "text-embedding-3-small",
			UpdateDelay:    new(300),
			MaxMemories:    new(5),
			Options: &agent_yaml.PromptMemoryOptions{
				UserProfileEnabled: new(true),
			},
		},
	}

	props, err := PromptAgentDefinitionToServiceProperties(original)
	require.NoError(t, err)
	require.Contains(t, props.AsMap(), "memory", "memory block must survive into azure.yaml")
	memory, ok := props.AsMap()["memory"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "gpt-4.1-mini", memory["chatModel"])
	require.Equal(t, "text-embedding-3-small", memory["embeddingModel"])
	require.Equal(t, float64(300), memory["updateDelay"])
	require.Equal(t, float64(5), memory["maxMemories"])
	require.NotContains(t, memory, "chat_model")
	options, ok := memory["options"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, options["userProfileEnabled"])
	require.NotContains(t, options, "user_profile_enabled")

	svc := &azdext.ServiceConfig{Name: "memory-agent", AdditionalProperties: props}
	got, found, err := PromptAgentFromResolvedService(svc, t.TempDir())
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, got.Memory, "memory block must survive the round trip")
	require.Equal(t, "conversation-store", got.Memory.Store)
}

// TestPromptAgentInlineRoundTripPreservesDefinition covers the fields the deploy
// path reads, so a marshaling change that silently drops one is caught here
// rather than at deploy time.
func TestPromptAgentInlineRoundTripPreservesDefinition(t *testing.T) {
	t.Parallel()

	temperature := 0.0
	topP := 0.9
	original := agent_yaml.PromptAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindPrompt,
			Name: "full-agent",
		},
		Model:        "gpt-4.1-mini",
		Instructions: "Be concise.",
		Harness: &agent_yaml.PromptHarness{
			Type: "github_copilot_preview",
		},
		Tools:       []any{map[string]any{"type": "code_interpreter"}},
		ToolChoice:  "auto",
		Temperature: &temperature,
		TopP:        &topP,
		Text:        map[string]any{"format": map[string]any{"type": "json_object"}},
		Reasoning:   map[string]any{"effort": "low"},
		StructuredInputs: map[string]any{
			"context": map[string]any{"required": false},
		},
		Connections: []string{"search"},
	}

	props, err := PromptAgentDefinitionToServiceProperties(original)
	require.NoError(t, err)
	for _, key := range []string{"toolChoice", "temperature", "topP", "text", "reasoning", "structuredInputs"} {
		require.Contains(t, props.AsMap(), key)
	}
	for _, key := range []string{"tool_choice", "top_p", "structured_inputs"} {
		require.NotContains(t, props.AsMap(), key)
	}

	svc := &azdext.ServiceConfig{Name: "full-agent", AdditionalProperties: props}
	got, found, err := PromptAgentFromResolvedService(svc, t.TempDir())
	require.NoError(t, err)
	require.True(t, found)

	require.Equal(t, agent_yaml.AgentKindPrompt, got.Kind)
	require.Equal(t, "full-agent", got.Name)
	require.Equal(t, "gpt-4.1-mini", got.Model)
	require.Equal(t, "Be concise.", got.Instructions)
	require.NotNil(t, got.Harness)
	require.Equal(t, "github_copilot_preview", got.Harness.Type)
	require.Len(t, got.Tools, 1)
	require.Equal(t, original.ToolChoice, got.ToolChoice)
	require.Equal(t, original.Temperature, got.Temperature)
	require.Equal(t, original.TopP, got.TopP)
	require.Equal(t, original.Text, got.Text)
	require.Equal(t, original.Reasoning, got.Reasoning)
	require.Equal(t, original.StructuredInputs, got.StructuredInputs)
	require.Len(t, got.Connections, 1)
	require.Equal(t, "search", got.Connections[0])

	// Never authored: the deploy graph resolves it from the skills/ folder.
}

func TestPromptAgentFromResolvedServiceInlineAndRootRefParity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	definition := `kind: prompt
name: parity-agent
model: gpt-4.1-mini
instructions: Be concise.
harness:
  type: github_copilot_preview
memory:
  store: conversation-store
  chatModel: gpt-4.1-mini
  embeddingModel: text-embedding-3-small
  updateDelay: 300
  maxMemories: 5
  options:
    chatSummaryEnabled: true
    userProfileEnabled: true
tools:
  - type: github_copilot_toolset_preview
    defaultConfig:
      enabled: false
    configs:
      - name: web
        enabled: true
policies:
  - type: rai_policy
    raiPolicyName: ${RAI_POLICY_ID}
    invocationsModeration:
      responseMode: streaming
      inputPaths:
        - $.input
      streamSelectors:
        - eventType: response.output_text.delta
          textField: $.delta
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "prompt.yaml"), []byte(definition), 0o600))

	inline := promptService(t, map[string]any{
		"kind":         "prompt",
		"name":         "parity-agent",
		"model":        "gpt-4.1-mini",
		"instructions": "Be concise.",
		"harness":      map[string]any{"type": "github_copilot_preview"},
		"memory": map[string]any{
			"store":          "conversation-store",
			"chatModel":      "gpt-4.1-mini",
			"embeddingModel": "text-embedding-3-small",
			"updateDelay":    300,
			"maxMemories":    5,
			"options": map[string]any{
				"chatSummaryEnabled": true,
				"userProfileEnabled": true,
			},
		},
		"tools": []any{
			map[string]any{
				"type":          "github_copilot_toolset_preview",
				"defaultConfig": map[string]any{"enabled": false},
				"configs":       []any{map[string]any{"name": "web", "enabled": true}},
			},
		},
		"policies": []any{
			map[string]any{
				"type":          "rai_policy",
				"raiPolicyName": "${RAI_POLICY_ID}",
				"invocationsModeration": map[string]any{
					"responseMode": "streaming",
					"inputPaths":   []any{"$.input"},
					"streamSelectors": []any{
						map[string]any{
							"eventType": "response.output_text.delta",
							"textField": "$.delta",
						},
					},
				},
			},
		},
	})
	referenced := promptService(t, map[string]any{"$ref": "./prompt.yaml"})

	inlineAgent, inlineFound, err := PromptAgentFromResolvedService(inline, root)
	require.NoError(t, err)
	require.True(t, inlineFound)
	refAgent, refFound, err := PromptAgentFromResolvedService(referenced, root)
	require.NoError(t, err)
	require.True(t, refFound)
	require.Equal(t, inlineAgent, refAgent)
	require.Equal(t, "${RAI_POLICY_ID}", refAgent.Policies[0].RaiPolicyName)
	require.Equal(t, "streaming", refAgent.Policies[0].InvocationsModeration.ResponseMode)
	require.Equal(
		t,
		"response.output_text.delta",
		refAgent.Policies[0].InvocationsModeration.StreamSelectors[0].EventType,
	)
	request, err := agent_yaml.CreatePromptAgentAPIRequest(refAgent, nil)
	require.NoError(t, err)
	apiDefinition, ok := request.Definition.(agent_api.ManagedAgentDefinition)
	require.True(t, ok)
	require.Len(t, apiDefinition.Tools, 1)
	apiTool, ok := apiDefinition.Tools[0].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, apiTool, "defaultConfig")
	require.Equal(t, map[string]any{"enabled": false}, apiTool["default_config"])
	require.Equal(
		t,
		"response.output_text.delta",
		apiDefinition.RaiConfig.InvocationsModeration.StreamSelectors[0].EventType,
	)
}

// TestPromptAgentFromResolvedServiceIgnoresOtherKinds confirms a hosted or voice
// entry is reported as "not found" rather than as an error, so the hosted
// resolvers keep their turn.
func TestPromptAgentFromResolvedServiceIgnoresOtherKinds(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"hosted", "prompt-voice"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			svc := &azdext.ServiceConfig{
				Name: "other",
				AdditionalProperties: mustStruct(t, map[string]any{
					"kind": kind,
					"name": "other",
				}),
			}
			_, found, err := PromptAgentFromResolvedService(svc, t.TempDir())
			require.NoError(t, err)
			require.False(t, found)
		})
	}
}

func TestPromptAgentFromResolvedServiceSkillReferences(t *testing.T) {
	svc := &azdext.ServiceConfig{
		Name: "skill-agent",
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind":         "prompt",
			"model":        "gpt-4.1-mini",
			"instructions": "Be helpful.",
			"skills": []any{
				"local-skill",
				map[string]any{"name": "microsoft-foundry", "version": "1"},
			},
		}),
	}
	got, found, err := PromptAgentFromResolvedService(svc, t.TempDir())
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []agent_yaml.HarnessSkillRef{
		{Name: "local-skill"},
		{Name: "microsoft-foundry", Version: "1"},
	}, got.Skills)
}

func TestPromptAgentFromResolvedServiceRejectsMalformedSkills(t *testing.T) {
	tests := []struct {
		name  string
		skill map[string]any
		want  string
	}{
		{"misspelled name", map[string]any{"nam": "foo", "version": "1"}, `unknown field "nam"`},
		{"unknown field", map[string]any{"name": "foo", "extra": true}, `unknown field "extra"`},
		{"missing name", map[string]any{"version": "1"}, "requires a non-empty name"},
		{"empty name", map[string]any{"name": "", "version": "1"}, "requires a non-empty name"},
		{"blank name", map[string]any{"name": "   ", "version": "1"}, "requires a non-empty name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &azdext.ServiceConfig{
				Name: "skill-agent",
				AdditionalProperties: mustStruct(t, map[string]any{
					"kind":         "prompt",
					"model":        "test-model",
					"instructions": "Be helpful.",
					"skills":       []any{tt.skill},
				}),
			}
			_, _, err := PromptAgentFromResolvedService(svc, t.TempDir())
			require.ErrorContains(t, err, tt.want)
		})
	}
}

// TestPromptAgentFromResolvedServiceNoDefinition confirms an entry carrying no
// definition at all falls through quietly, which is what lets projects that
// still keep their definition in a file reach the file-based path.
func TestPromptAgentFromResolvedServiceNoDefinition(t *testing.T) {
	t.Parallel()

	svc := &azdext.ServiceConfig{
		Name: "no-definition",
	}
	_, found, err := PromptAgentFromResolvedService(svc, t.TempDir())
	require.NoError(t, err)
	require.False(t, found)
}

// TestPromptAgentEffectiveStrictValidation is the regression test for the
// validation gap created when service properties bypass typed YAML decoding.
//
// The strict checks on harness: and memory: live in UnmarshalYAML, which never
// runs for the effective inline/root-ref property map. Without the explicit
// validation pass these definitions would deploy an agent whose capabilities
// differ from what was authored.
func TestPromptAgentEffectiveStrictValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{
			name: "harness as a string is rejected",
			props: map[string]any{
				"kind":    "prompt",
				"name":    "a",
				"harness": "github_copilot_preview",
			},
			wantErr: "harness must be a block",
		},
		{
			name: "obsolete harness configuration is rejected",
			props: map[string]any{
				"kind": "prompt",
				"name": "a",
				"harness": map[string]any{
					"type":          "github_copilot_preview",
					"builtin_tools": map[string]any{"excluded": []any{"shell"}},
				},
			},
			wantErr: "builtin_tools",
		},
		{
			name: "memory typo binds nothing and is rejected",
			props: map[string]any{
				"kind":   "prompt",
				"name":   "a",
				"memory": map[string]any{"stores": "s"},
			},
			wantErr: "stores",
		},
		{
			name: "snake case memory property is rejected",
			props: map[string]any{
				"kind":   "prompt",
				"name":   "a",
				"memory": map[string]any{"store": "s", "chat_model": "gpt-4.1-mini"},
			},
			wantErr: "chat_model",
		},
		{
			name: "snake case memory option is rejected",
			props: map[string]any{
				"kind": "prompt",
				"name": "a",
				"memory": map[string]any{
					"store":   "s",
					"options": map[string]any{"default_ttl_seconds": 3600},
				},
			},
			wantErr: "default_ttl_seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, source := range []string{"inline", "root-ref"} {
				t.Run(source, func(t *testing.T) {
					var svc *azdext.ServiceConfig
					root := t.TempDir()
					if source == "inline" {
						svc = promptService(t, tt.props)
					} else {
						data, err := yaml.Marshal(tt.props)
						require.NoError(t, err)
						require.NoError(t, os.WriteFile(filepath.Join(root, "prompt.yaml"), data, 0o600))
						svc = promptService(t, map[string]any{"$ref": "./prompt.yaml"})
					}

					_, _, err := PromptAgentFromResolvedService(svc, root)
					require.Error(t, err)
					require.Contains(t, err.Error(), tt.wantErr)
				})
			}
		})
	}
}

// TestPromptAgentInlineAcceptsPassThroughTools confirms the forward-compatible
// fields stay forward-compatible: a tool type newer than this build must not be
// rejected by the strict pass.
func TestPromptAgentInlineAcceptsPassThroughTools(t *testing.T) {
	t.Parallel()

	svc := &azdext.ServiceConfig{
		Name: "a",
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind":  "prompt",
			"name":  "a",
			"model": "gpt-4.1-mini",
			"tools": []any{map[string]any{"type": "some_future_tool_preview", "unknown": true}},
		}),
	}

	got, found, err := PromptAgentFromResolvedService(svc, t.TempDir())
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, got.Tools, 1)
}

// TestResolvePromptAgentSettingsFromEnvironment verifies the harness target
// resolves entirely from the azd environment.
func TestResolvePromptAgentSettingsFromEnvironment(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"AZURE_SUBSCRIPTION_ID":    "sub-1",
		"AZURE_RESOURCE_GROUP":     "rg-1",
		"FOUNDRY_PROJECT_ENDPOINT": "https://proj.services.ai.azure.com/api/projects/p",
	}

	settings, err := ResolvePromptAgentSettings(env)
	require.NoError(t, err)
	require.Equal(t, "sub-1", settings.SubscriptionID)
	require.Equal(t, "rg-1", settings.ResourceGroup)
	require.Equal(t, "https://proj.services.ai.azure.com/api/projects/p", settings.ProjectEndpoint)
}

// TestServiceIsPromptAgent requires an explicit prompt kind.
func TestServiceIsPromptAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		svc  *azdext.ServiceConfig
		want bool
	}{
		{
			name: "inline prompt definition",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"kind": "prompt", "name": "a"}),
			},
			want: true,
		},
		{
			name: "inline hosted definition",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"kind": "hosted", "name": "a"}),
			},
			want: false,
		},
		{
			name: "inline voice definition",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"kind": "prompt-voice", "name": "a"}),
			},
			want: false,
		},
		{
			name: "no definition and no block",
			svc:  &azdext.ServiceConfig{},
			want: false,
		},
		{
			name: "nil service",
			svc:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, ServiceIsPromptAgent(tt.svc))
		})
	}
}
