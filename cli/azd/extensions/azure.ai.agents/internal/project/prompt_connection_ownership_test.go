// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestAgentConnectionReferencesConfigAndSchema(t *testing.T) {
	t.Parallel()
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	for _, tt := range []struct {
		name        string
		connections any
		want        []string
		validPrompt bool
	}{
		{name: "names", connections: []any{"search", "api"}, want: []string{"search", "api"}, validPrompt: true},
		{name: "empty array", connections: []any{}, want: []string{}, validPrompt: true},
		{name: "null", connections: nil},
		{name: "scalar", connections: "search"},
		{name: "object", connections: map[string]any{"name": "search"}},
		{name: "name-only object", connections: []any{map[string]any{"name": "search"}}},
		{name: "resource object", connections: []any{map[string]any{
			"name": "search", "category": "CognitiveSearch", "target": "https://search.test", "authType": "AAD",
		}}},
		{name: "mixed array", connections: []any{"api", map[string]any{"name": "search"}}},
		{name: "empty name", connections: []any{""}},
		{name: "blank name", connections: []any{" \t\n"}},
		{name: "number", connections: []any{42}},
		{name: "null entry", connections: []any{nil}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, kind := range []string{"", "hosted", "prompt", "prompt-voice", "voice"} {
				t.Run("kind="+kind, func(t *testing.T) {
					t.Parallel()
					values := map[string]any{"connections": tt.connections}
					if kind != "" {
						values["kind"] = kind
					}
					if kind == "prompt" {
						values["model"] = "gpt-5-mini"
						values["instructions"] = "Be helpful."
						values["name"] = "assistant"
					} else if kind == "prompt-voice" || kind == "voice" {
						values["model"] = map[string]any{"id": "gpt-realtime"}
					}
					valid := kind == "prompt" && tt.validPrompt
					if valid {
						require.NoError(t, schema.validate(values))
					} else {
						require.Error(t, schema.validate(values))
					}
					for _, shape := range []string{"inline", "legacy", "file reference"} {
						t.Run(shape, func(t *testing.T) {
							t.Parallel()
							root := t.TempDir()
							props := mustStruct(t, values)
							svc := &azdext.ServiceConfig{
								Name: "assistant", Host: foundryAgentHost, AdditionalProperties: props,
							}
							switch shape {
							case "legacy":
								svc.Config, svc.AdditionalProperties = props, nil
							case "file reference":
								data, err := json.Marshal(values)
								require.NoError(t, err)
								definitionPath := filepath.Join(root, "definition.json")
								require.NoError(t, os.WriteFile(definitionPath, data, 0o600))
								svc.AdditionalProperties = mustStruct(t, map[string]any{
									"$ref": "./definition.json",
								})
								require.NoError(t, ResolveServiceConfigInPlace(svc, root))
							}
							before := proto.Clone(svc)
							cfg, err := LoadServiceTargetAgentConfig(svc)
							require.True(t, proto.Equal(before, svc), "loading config must preserve prompt references")
							if !valid {
								require.ErrorContains(t, err, "connections")
								return
							}
							require.NoError(t, err)
							require.Empty(t, cfg.Connections, "prompt names are not generic resource definitions")
							managed, found, err := PromptAgentFromResolvedService(svc, root)
							require.NoError(t, err)
							require.True(t, found)
							require.Equal(t, tt.want, managed.Connections)
							graph, err := newPromptGraph(root, &managed, nil, nil, nil)
							require.NoError(t, err)
							var connectionNodes int
							for _, node := range graph.nodes {
								if node.Kind == nodeConnection {
									connectionNodes++
								}
								if node.Validate != nil {
									require.NoError(t, node.Validate())
								}
							}
							if len(tt.want) > 0 {
								require.Equal(t, 1, connectionNodes, "prompt graph must still validate connections")
							} else {
								require.Zero(t, connectionNodes)
							}
						})
					}
				})
			}
		})
	}
}

func TestPromptAgentFromResolvedServiceRejectsConnectionObjects(t *testing.T) {
	t.Parallel()
	for _, entry := range []map[string]any{
		{"name": "search"},
		{"name": "search", "category": "CognitiveSearch", "target": "https://search.test", "authType": "AAD"},
	} {
		svc := &azdext.ServiceConfig{
			Name: "assistant", Host: foundryAgentHost,
			AdditionalProperties: mustStruct(t, map[string]any{
				"kind": "prompt", "model": "gpt-5-mini", "instructions": "Be helpful.",
				"connections": []any{entry},
			}),
		}
		// Prompt deploy dispatches to its own loader before hosted validation.
		// It must reject objects too, not just the shared service config loader.
		_, _, err := PromptAgentFromResolvedService(svc, t.TempDir())
		require.ErrorContains(t, err, "connections")
	}
}

func TestPromptConnectionReferencesPreserveGraphValidation(t *testing.T) {
	t.Parallel()
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	for _, tt := range []struct {
		name, field string
		value       any
		omit        bool
	}{
		{name: "missing model", field: "model", omit: true},
		{name: "blank model", field: "model", value: " \t"},
		{name: "missing instructions", field: "instructions", omit: true},
		{name: "blank instructions", field: "instructions", value: " \t"},
		{name: "tool without type", field: "tools", value: []any{map[string]any{"name": "invalid"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values := map[string]any{
				"kind": "prompt", "name": "assistant", "model": "gpt-5-mini", "instructions": "Be helpful.",
				"connections": []any{"search"},
			}
			if tt.omit {
				delete(values, tt.field)
			} else {
				values[tt.field] = tt.value
			}
			svc := &azdext.ServiceConfig{
				Name: "assistant", Host: foundryAgentHost, AdditionalProperties: mustStruct(t, values),
			}
			// The shared loader checks resource ownership, not the prompt contract.
			_, err := LoadServiceTargetAgentConfig(svc)
			require.NoError(t, err)
			root := t.TempDir()
			managed, found, err := PromptAgentFromResolvedService(svc, root)
			require.NoError(t, err)
			require.True(t, found)
			graph, err := newPromptGraph(root, &managed, nil, nil, nil)
			require.NoError(t, err)
			resolved := false
			for i := range graph.nodes {
				graph.nodes[i].Resolve = func(context.Context) error { resolved = true; return nil }
			}
			require.ErrorContains(t, graph.resolve(t.Context(), nil), tt.field)
			require.False(t, resolved, "the entire prompt graph must validate before any resolution")
			require.Error(t, schema.validate(values))
		})
	}
}

func TestAgentSchemaDoesNotRestoreBundledDefinitions(t *testing.T) {
	t.Parallel()
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	definitions, ok := schema.root["definitions"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, definitions, "Connection")
	require.NotContains(t, definitions, "Toolbox")
	data, err := json.Marshal(schema.root)
	require.NoError(t, err)
	require.NotContains(t, string(data), `#/definitions/Connection`)
	require.NotContains(t, string(data), `#/definitions/Toolbox`)
}
