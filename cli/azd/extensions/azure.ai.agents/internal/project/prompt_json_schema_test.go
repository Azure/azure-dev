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

func TestPromptJSONSchemaRequiresPromptInstructions(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	require.Error(t, schema.validate(map[string]any{
		"kind":  "prompt",
		"name":  "assistant",
		"model": "gpt-5-mini",
	}))
}

func TestPromptJSONSchemaPreservesVoiceAndLegacyConfig(t *testing.T) {
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	require.NoError(t, schema.validate(map[string]any{
		"kind":  "prompt-voice",
		"model": map[string]any{"id": "gpt-realtime"},
		"promptAgent": map[string]any{
			"baseUrl":   "https://legacy.example.com",
			"workspace": "legacy-workspace",
		},
	}))
}
