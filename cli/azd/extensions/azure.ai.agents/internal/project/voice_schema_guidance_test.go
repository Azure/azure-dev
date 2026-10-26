// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVoiceSchemaAuthoringGuidance(t *testing.T) {
	t.Parallel()
	schema := loadDocSchema(t, extensionRoot(t))
	modelType, ok := schema.property("modelType")
	require.True(t, ok)
	require.Equal(t, []any{"managed", "self_deployed"}, modelType["enum"])
	require.Contains(t, modelType["description"], "conversationEngine")
	target, ok := schema.property("targetAgent")
	require.True(t, ok)
	require.Equal(t, true, target["deprecated"])
	require.Contains(t, target["description"], "Unsupported")
	require.Contains(t, target["description"], "conversationEngine")
	target = schema.resolve(target)
	require.NotContains(t, target, "properties", "removed target must not offer service/version completion")

	for _, kind := range []string{"voice", "prompt-voice"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, mode := range []string{"managed", "self_deployed"} {
				require.NoError(t, schema.validate(map[string]any{
					"kind": kind, "modelType": mode, "model": map[string]any{"id": "example-model"},
				}))
			}
			require.NoError(t, schema.validate(map[string]any{
				"kind": kind, "model": map[string]any{"id": "gpt-realtime"},
			}))
			require.NoError(t, schema.validate(map[string]any{
				"kind": kind, "conversationEngine": map[string]any{"type": "hosted_agent", "name": "target"},
			}))
			for _, legacy := range []map[string]any{
				{"modelType": "hosted_agent"},
				{"targetAgent": map[string]any{}},
				{"targetAgent": map[string]any{"service": "target"}},
				{"targetAgent": nil},
				{"modelType": "hosted_agent", "targetAgent": map[string]any{"service": "target"}},
				{"modelType": "managed", "targetAgent": map[string]any{"service": "target"}},
				{"modelType": "self_deployed", "targetAgent": map[string]any{"service": "target"}},
			} {
				legacy["kind"] = kind
				legacy["model"] = map[string]any{"id": "gpt-realtime"}
				require.Error(t, schema.validate(legacy), "old authoring must remain rejected: %v", legacy)
			}
		})
	}
	require.NoError(t, schema.validate(map[string]any{
		"kind": "voice", "subProtocol": "live", "modelType": "self_deployed",
		"model": map[string]any{"id": "gpt-live-1"},
		"audio": map[string]any{
			"format": map[string]any{"type": "audio/pcm", "rate": 24000},
			"output": map[string]any{"voice": "marin"},
		},
		"delegation": map[string]any{"type": "client"},
	}))
	require.NoError(t, schema.validate(map[string]any{
		"kind": "voice", "subProtocol": "realtime", "model": map[string]any{"id": "gpt-realtime"},
		"audio": map[string]any{},
	}))
	require.NoError(t, schema.validate(map[string]any{
		"kind": "voice", "model": map[string]any{"id": "gpt-realtime"},
		"audio": map[string]any{"output": map[string]any{}},
	}))
	for _, invalid := range []map[string]any{
		{"kind": "prompt-voice", "subProtocol": "live", "modelType": "self_deployed", "model": map[string]any{"id": "gpt-live-1"}},
		{"kind": "voice", "subProtocol": "live", "modelType": "managed", "model": map[string]any{"id": "gpt-live-1"}},
		{"kind": "voice", "subProtocol": "live", "modelType": "self_deployed", "model": map[string]any{"id": "gpt-live-1"}, "avatar": map[string]any{}},
		{"kind": "voice", "model": map[string]any{"id": "gpt-realtime"}, "delegation": map[string]any{"type": "client"}},
		{"kind": "voice", "subProtocol": "realtime", "model": map[string]any{"id": "gpt-realtime"}, "audio": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": 24000}}},
		{
			"kind":               "voice",
			"conversationEngine": map[string]any{"type": "hosted_agent", "name": "target"},
			"audio": map[string]any{
				"format": map[string]any{"type": "audio/pcm", "rate": 24000},
				"output": map[string]any{"voice": "marin"},
			},
		},
	} {
		require.Error(t, schema.validate(invalid), "invalid cross-protocol definition: %v", invalid)
	}
	require.NoError(t, schema.validate(map[string]any{
		"kind": "prompt", "name": "prompt-agent", "model": "my-deployment", "instructions": "Be helpful.",
	}))
	require.NoError(t, schema.validate(map[string]any{
		"kind": "hosted", "name": "target",
		"codeConfiguration": map[string]any{"runtime": "python_3_13", "entryPoint": "app.py"},
		"protocols":         []any{map[string]any{"protocol": "responses", "version": "1.0"}},
	}))
}
