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
				"greeting": map[string]any{"type": "template", "text": "Hello"},
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
		"kind": "prompt", "name": "prompt-agent", "model": "my-deployment", "instructions": "Be helpful.",
	}))
	require.NoError(t, schema.validate(map[string]any{
		"kind": "hosted", "name": "target",
		"codeConfiguration": map[string]any{"runtime": "python_3_13", "entryPoint": "app.py"},
		"protocols":         []any{map[string]any{"protocol": "responses", "version": "1.0"}},
	}))

	handoff, ok := schema.property("handoff")
	require.True(t, ok)
	require.EqualValues(t, 0, handoff["maxProperties"])
	require.NoError(t, schema.validate(map[string]any{
		"kind": "voice", "model": map[string]any{"id": "gpt-realtime"}, "handoff": map[string]any{},
	}))
	require.Error(t, schema.validate(map[string]any{
		"kind": "voice", "model": map[string]any{"id": "gpt-realtime"},
		"handoff": map[string]any{"target": "agent"},
	}))

	voice := schema.resolve(map[string]any{"$ref": "#/definitions/VoiceConfig"})
	rate := voice["properties"].(map[string]any)["rate"].(map[string]any)
	require.Equal(t, []any{""}, rate["enum"])

	for _, definition := range []map[string]any{
		{"kind": "voice", "modelType": "managed", "model": map[string]any{"id": "gpt-realtime"}},
		{"kind": "voice", "modelType": "self_deployed", "model": map[string]any{"id": "deployment"}},
		{"kind": "voice", "conversationEngine": map[string]any{"type": "hosted_agent", "name": "target"},
			"greeting": map[string]any{"text": "Hello"}},
	} {
		for _, field := range []string{"inputSchema", "outputSchema"} {
			definition[field] = map[string]any{}
			require.Error(t, schema.validate(definition), "%s must be excluded from %v", field, definition)
			delete(definition, field)
		}
		for _, field := range []string{
			"subagent_config", "subagentConfig", "interim_response", "interimResponse",
		} {
			definition[field] = map[string]any{}
			require.Error(t, schema.validate(definition), "%s must be excluded from %v", field, definition)
			delete(definition, field)
		}
	}
	hostedWrapper := map[string]any{
		"kind": "voice", "conversationEngine": map[string]any{"type": "hosted_agent", "name": "target"},
	}
	for _, field := range []string{
		"codeConfiguration", "sessionConfiguration", "environmentVariables", "policies", "protocols", "toolbox",
	} {
		hostedWrapper[field] = map[string]any{}
		require.Error(t, schema.validate(hostedWrapper), "%s must be excluded from hosted voice", field)
		delete(hostedWrapper, field)
	}
}
