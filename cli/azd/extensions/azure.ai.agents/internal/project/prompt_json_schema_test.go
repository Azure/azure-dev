// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"maps"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPromptJSONSchemaAcceptsInlinePrompt(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	require.NoError(t, schema.validate(map[string]any{
		"kind":         "prompt",
		"name":         "assistant",
		"model":        "gpt-5-mini",
		"instructions": "Be helpful.",
	}))
}

func TestPromptJSONSchemaAcceptsMicrosoftCopilotToolset(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	for _, harness := range []string{"microsoft_copilot_preview", "github_copilot_preview"} {
		for _, toolType := range []string{"microsoft_copilot_toolset_preview", "github_copilot_toolset_preview"} {
			t.Run(harness+"/"+toolType, func(t *testing.T) {
				tool := map[string]any{
					"type":          toolType,
					"defaultConfig": map[string]any{"enabled": false},
					"configs":       []any{map[string]any{"name": "web", "enabled": true}},
				}
				definition := map[string]any{
					"kind":         "prompt",
					"name":         "assistant",
					"model":        "gpt-5-mini",
					"instructions": "Be helpful.",
					"harness":      map[string]any{"type": harness},
					"tools":        []any{tool},
				}
				require.NoError(t, schema.validate(definition))

				delete(tool, "defaultConfig")
				tool["default_config"] = map[string]any{"enabled": false}
				require.Error(t, schema.validate(definition), "known toolsets must not use the permissive fallback")
			})
		}
	}
}

func TestPromptJSONSchemaAcceptsPromptControls(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	require.NoError(t, schema.validate(map[string]any{
		"kind":             "prompt",
		"name":             "assistant",
		"model":            "gpt-5-mini",
		"instructions":     "Be helpful.",
		"toolChoice":       "auto",
		"temperature":      0.0,
		"topP":             0.9,
		"text":             map[string]any{"format": map[string]any{"type": "json_object"}},
		"reasoning":        map[string]any{"effort": "low"},
		"structuredInputs": map[string]any{"context": map[string]any{"required": false}},
	}))
}

func TestPromptJSONSchemaUsesConnectionServiceReferences(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	prompt := map[string]any{
		"kind":         "prompt",
		"name":         "assistant",
		"model":        "gpt-5-mini",
		"instructions": "Be helpful.",
		"connections":  []any{"search-connection", "api-connection"},
	}
	require.NoError(t, schema.validate(prompt))

	prompt["connections"] = []any{map[string]any{
		"name": "search-connection", "category": "CognitiveSearch", "target": "https://example.com", "authType": "AAD",
	}}
	require.Error(t, schema.validate(prompt))
}

func TestHostedJSONSchemaRejectsConnectionResources(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	require.Error(t, schema.validate(map[string]any{
		"kind": "hosted",
		"connections": []any{map[string]any{
			"name": "search-connection", "category": "CognitiveSearch", "target": "https://example.com", "authType": "AAD",
		}},
	}))
}

func TestPromptJSONSchemaAcceptsToolboxesAndRejectsSingularToolbox(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	base := map[string]any{
		"kind":         "prompt",
		"name":         "assistant",
		"model":        "gpt-5-mini",
		"instructions": "Be helpful.",
		"harness":      map[string]any{"type": "microsoft_copilot_preview"},
	}

	base["toolboxes"] = []any{"research-tools", map[string]any{"name": "support-tools"}}
	require.NoError(t, schema.validate(base))

	delete(base, "toolboxes")
	base["toolbox"] = map[string]any{"name": "legacy-tools"}
	require.Error(t, schema.validate(base))
}

func TestPromptJSONSchemaValidatesMemory(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	base := map[string]any{
		"kind": "prompt", "name": "assistant", "model": "gpt-5-mini", "instructions": "Be helpful.",
	}
	base["memory"] = map[string]any{
		"store":          "agent-memory",
		"chatModel":      "gpt-5-mini",
		"embeddingModel": "text-embedding-3-small",
		"updateDelay":    300,
		"maxMemories":    5,
		"options": map[string]any{
			"chatSummaryEnabled":      true,
			"userProfileEnabled":      true,
			"proceduralMemoryEnabled": false,
			"defaultTtlSeconds":       3600,
			"userProfileDetails":      "Remember stable preferences.",
		},
	}
	require.NoError(t, schema.validate(base))

	base["memory"] = map[string]any{"store": "agent-memory", "unknown": true}
	require.Error(t, schema.validate(base))
}

func TestPromptJSONSchemaRejectsSnakeCaseAuthoringKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value map[string]any
	}{
		{"chat model", map[string]any{"memory": map[string]any{"store": "m", "chat_model": "chat"}}},
		{"embedding model", map[string]any{"memory": map[string]any{"store": "m", "embedding_model": "embedding"}}},
		{"update delay", map[string]any{"memory": map[string]any{"store": "m", "update_delay": 300}}},
		{"max memories", map[string]any{"memory": map[string]any{"store": "m", "max_memories": 5}}},
		{"chat summary", map[string]any{"memory": map[string]any{
			"store": "m", "options": map[string]any{"chat_summary_enabled": true},
		}}},
		{"user profile", map[string]any{"memory": map[string]any{
			"store": "m", "options": map[string]any{"user_profile_enabled": true},
		}}},
		{"procedural memory", map[string]any{"memory": map[string]any{
			"store": "m", "options": map[string]any{"procedural_memory_enabled": true},
		}}},
		{"default ttl", map[string]any{"memory": map[string]any{
			"store": "m", "options": map[string]any{"default_ttl_seconds": 3600},
		}}},
		{"user profile details", map[string]any{"memory": map[string]any{
			"store": "m", "options": map[string]any{"user_profile_details": "details"},
		}}},
		{"default config", map[string]any{"tools": []any{map[string]any{
			"type": "microsoft_copilot_toolset_preview", "default_config": map[string]any{"enabled": false},
		}}}},
	}

	schema := loadDocSchema(t, filepath.Join("..", ".."))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			value := map[string]any{
				"kind": "prompt", "name": "assistant", "model": "gpt-5-mini", "instructions": "Be helpful.",
			}
			maps.Copy(value, test.value)
			require.Error(t, schema.validate(value))
		})
	}
}

func TestPromptJSONSchemaRejectsHarnessConfiguration(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	require.Error(t, schema.validate(map[string]any{
		"kind":         "prompt",
		"name":         "assistant",
		"model":        "gpt-5-mini",
		"instructions": "Be helpful.",
		"harness": map[string]any{
			"type":          "microsoft_copilot_preview",
			"builtin_tools": map[string]any{"allowed": []any{"web"}},
		},
	}))
}

func TestPromptJSONSchemaRequiresPromptInstructions(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	require.Error(t, schema.validate(map[string]any{
		"kind":  "prompt",
		"name":  "assistant",
		"model": "gpt-5-mini",
	}))
}

func TestPromptJSONSchemaPreservesVoiceModel(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	require.NoError(t, schema.validate(map[string]any{
		"kind":  "prompt-voice",
		"model": map[string]any{"id": "gpt-realtime"},
	}))
}
