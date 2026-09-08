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
