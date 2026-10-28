// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestVoiceDefinitionOverlayServicePropertiesRoundTrip(t *testing.T) {
	overlay := map[string]any{"new_field": map[string]any{"enabled": true}}
	props, err := VoiceAgentDefinitionToServiceProperties(agent_yaml.VoiceAgent{
		AgentDefinition:   agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindVoice, Name: "voice"},
		Model:             &agent_yaml.Model{Id: "gpt-realtime"},
		DefinitionOverlay: overlay,
	}, nil)
	require.NoError(t, err)
	voice, found, err := VoiceAgentFromResolvedService(&azdext.ServiceConfig{
		Name: "voice", AdditionalProperties: props,
	}, t.TempDir())
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, overlay, voice.DefinitionOverlay)
}

func TestVoiceDefinitionOverlayRefsResolveRelativeToContainingDocument(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "definitions"), 0o750))
	require.NoError(t, os.Mkdir(filepath.Join(root, "definitions", "parts"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "definitions", "parts", "overlay.yaml"),
		[]byte("future:\n  enabled: true\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "definitions", "voice.yaml"), []byte(`
kind: voice
name: voice
model: {id: gpt-realtime}
definition_overlay:
  $ref: ./parts/overlay.yaml
`), 0o600))

	voice, found, err := VoiceAgentFromResolvedService(&azdext.ServiceConfig{
		Name: "voice",
		AdditionalProperties: mustStruct(t, map[string]any{
			"$ref": "./definitions/voice.yaml",
		}),
	}, root)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, true, voice.DefinitionOverlay["future"].(map[string]any)["enabled"])
}

func TestVoiceDefinitionOverlayInlineRefResolvesRelativeToProject(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "overlay.json"),
		[]byte(`{"future":{"enabled":true}}`), 0o600))
	voice, found, err := VoiceAgentFromResolvedService(&azdext.ServiceConfig{
		Name: "voice",
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind": "voice", "name": "voice", "model": map[string]any{"id": "gpt-realtime"},
			"definitionOverlay": map[string]any{"$ref": "./overlay.json"},
		}),
	}, root)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, true, voice.DefinitionOverlay["future"].(map[string]any)["enabled"])
}

func TestVoiceDefinitionOverlayInvalidRefsUseExistingResolver(t *testing.T) {
	tests := []struct {
		name    string
		content string
		ref     string
	}{
		{name: "missing", ref: "./missing.yaml"},
		{name: "nonobject", content: "- item\n", ref: "./overlay.yaml"},
		{name: "remote", ref: "https://example.com/overlay.yaml"},
		{name: "cycle", content: "$ref: ./overlay.yaml\n", ref: "./overlay.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.content != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, "overlay.yaml"), []byte(tt.content), 0o600))
			}
			_, _, err := VoiceAgentFromResolvedService(&azdext.ServiceConfig{
				Name: "voice",
				AdditionalProperties: mustStruct(t, map[string]any{
					"kind": "voice", "name": "voice", "model": map[string]any{"id": "gpt-realtime"},
					"definitionOverlay": map[string]any{"$ref": tt.ref},
				}),
			}, root)
			require.Error(t, err)
		})
	}
}

func TestDefinitionOverlayRejectsNonVoiceAgents(t *testing.T) {
	for _, kind := range []string{"prompt", "hosted"} {
		for _, key := range []string{"definitionOverlay", "definition_overlay"} {
			t.Run(kind+"/"+key, func(t *testing.T) {
				_, _, _, _, err := AgentDefinitionFromService(&azdext.ServiceConfig{
					Name: "agent",
					AdditionalProperties: mustStruct(t, map[string]any{
						"kind": kind, key: map[string]any{"future": true},
					}),
				})
				require.ErrorContains(t, err, "definitionOverlay is only supported on voice agents")
				if kind == "prompt" {
					_, _, err = PromptAgentFromResolvedService(&azdext.ServiceConfig{
						Name: "agent", AdditionalProperties: mustStruct(t, map[string]any{
							"kind": kind, key: map[string]any{},
						}),
					}, t.TempDir())
					require.ErrorContains(t, err, "definitionOverlay is only supported on voice agents")
				}
			})
		}
	}
}

func TestVoiceDefinitionOverlayShapeAndAliases(t *testing.T) {
	for _, value := range []any{nil, "bad", []any{"bad"}} {
		_, _, err := VoiceAgentFromResolvedService(&azdext.ServiceConfig{
			Name: "voice", AdditionalProperties: mustStruct(t, map[string]any{
				"kind": "voice", "definitionOverlay": value,
			}),
		}, t.TempDir())
		require.ErrorContains(t, err, "definitionOverlay must be an object")
	}
	props := mustStruct(t, map[string]any{
		"kind": "voice", "name": "voice", "model": map[string]any{"id": "gpt-realtime"},
		"definition_overlay": map[string]any{"future": true},
	})
	for range 2 {
		voice, found, err := VoiceAgentFromResolvedService(&azdext.ServiceConfig{
			Name: "voice", AdditionalProperties: props,
		}, t.TempDir())
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, true, voice.DefinitionOverlay["future"])
		require.NotContains(t, props.GetFields(), "definitionOverlay")
	}
	both := props.AsMap()
	both["definitionOverlay"] = map[string]any{}
	_, _, err := VoiceAgentFromResolvedService(&azdext.ServiceConfig{
		Name: "voice", AdditionalProperties: mustStruct(t, both),
	}, t.TempDir())
	require.ErrorContains(t, err, "cannot specify both")
}

func TestDirectPromptDefinitionRejectsDefinitionOverlay(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "prompt.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
kind: prompt
name: prompt
model: gpt-4.1
instructions: help
definition_overlay: {}
`), 0o600))
	_, err := LoadAgentDefinitionFile(root, path, "prompt")
	require.ErrorContains(t, err, "definitionOverlay is only supported on voice agents")
}
