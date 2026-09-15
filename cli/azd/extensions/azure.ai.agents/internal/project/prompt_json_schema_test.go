// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
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

func TestPromptJSONSchemaAcceptsCopilotToolset(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	require.NoError(t, schema.validate(map[string]any{
		"kind":         "prompt",
		"name":         "assistant",
		"model":        "gpt-5-mini",
		"instructions": "Be helpful.",
		"harness":      map[string]any{"type": "github_copilot_preview"},
		"tools": []any{map[string]any{
			"type":           "github_copilot_toolset_preview",
			"default_config": map[string]any{"enabled": false},
			"configs":        []any{map[string]any{"name": "web", "enabled": true}},
		}},
	}))
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

func TestPromptJSONSchemaAcceptsOptionalToolboxProjectConnectionID(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	base := map[string]any{
		"kind":         "prompt",
		"name":         "assistant",
		"model":        "gpt-5-mini",
		"instructions": "Be helpful.",
		"harness":      map[string]any{"type": "github_copilot_preview"},
	}

	base["toolbox"] = map[string]any{"name": "public-tools"}
	require.NoError(t, schema.validate(base))

	base["toolbox"] = map[string]any{
		"name": "authenticated-tools", "projectConnectionId": "toolbox-auth",
	}
	require.NoError(t, schema.validate(base))

	base["toolbox"] = map[string]any{"name": "legacy-tools", "connection": "toolbox-auth"}
	require.Error(t, schema.validate(base))
}

func TestPromptJSONSchemaValidatesMemory(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	base := map[string]any{
		"kind": "prompt", "name": "assistant", "model": "gpt-5-mini", "instructions": "Be helpful.",
	}
	base["memory"] = map[string]any{
		"store": "agent-memory", "chat_model": "gpt-5-mini", "embedding_model": "text-embedding-3-small",
		"options": map[string]any{"chat_summary_enabled": true},
	}
	require.NoError(t, schema.validate(base))

	base["memory"] = map[string]any{"store": "agent-memory", "unknown": true}
	require.Error(t, schema.validate(base))
}

func TestPromptJSONSchemaRejectsHarnessConfiguration(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	require.Error(t, schema.validate(map[string]any{
		"kind":         "prompt",
		"name":         "assistant",
		"model":        "gpt-5-mini",
		"instructions": "Be helpful.",
		"harness": map[string]any{
			"type":          "github_copilot_preview",
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
