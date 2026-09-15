// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestCreateAgentAPIRequestMetadataTags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		tags  any
		want  string
		found bool
	}{
		{name: "YAML list", tags: []any{"Streaming", "Test"}, want: `["Streaming","Test"]`, found: true},
		{name: "typed list", tags: []string{"Streaming", "Test"}, want: `["Streaming","Test"]`, found: true},
		{name: "canonical set", tags: []any{"Test", "Streaming", "Test"}, want: `["Streaming","Test"]`, found: true},
		{name: "commas and quotes", tags: []string{`A, B`, `Say "hello"`},
			want: `["A, B","Say \"hello\""]`, found: true},
		{name: "legacy string", tags: "Streaming,Test", want: "Streaming,Test", found: true},
		{name: "empty list", tags: []any{}},
		{name: "nil slice", tags: []string(nil)},
		{name: "empty string", tags: ""},
		{name: "maximum value", tags: []string{strings.Repeat("a", 508)},
			want: `["` + strings.Repeat("a", 508) + `"]`, found: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			metadata := map[string]any{"tags": tt.tags, "owner": "team", "authors": []any{"Alice", "Bob"}}
			original, err := json.Marshal(metadata)
			require.NoError(t, err)
			request, err := createAgentAPIRequest(AgentDefinition{
				Kind: AgentKindHosted, Name: "agent", Metadata: &metadata,
			}, nil, nil, nil)
			require.NoError(t, err)
			got, found := request.Metadata["tags"]
			assert.Equal(t, tt.found, found)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, "team", request.Metadata["owner"])
			assert.Equal(t, "Alice,Bob", request.Metadata["authors"])
			after, err := json.Marshal(metadata)
			require.NoError(t, err)
			assert.JSONEq(t, string(original), string(after), "mapping must not mutate authored metadata")
		})
	}
}

func TestCreateAgentAPIRequestRejectsInvalidMetadataTags(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		tags any
	}{
		{name: "number", tags: 42},
		{name: "object", tags: map[string]any{"tag": "Test"}},
		{name: "mixed list", tags: []any{"Test", 42}},
		{name: "null", tags: nil},
		{name: "null entry", tags: []any{"Test", nil}},
		{name: "empty entry", tags: []string{""}},
		{name: "blank entry", tags: []string{" \t"}},
		{name: "list too long", tags: []string{strings.Repeat("a", 509)}},
		{name: "string too long", tags: strings.Repeat("a", 513)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request, err := createAgentAPIRequest(AgentDefinition{
				Kind: AgentKindHosted, Name: "agent", Metadata: new(map[string]any{"tags": tt.tags}),
			}, nil, nil, nil)
			require.ErrorContains(t, err, "metadata.tags")
			assert.Nil(t, request, "invalid tags must not be silently dropped")
		})
	}
}

func TestHostedAgentMetadataTagsSurviveWireRequest(t *testing.T) {
	t.Parallel()
	var definition ContainerAgent
	require.NoError(t, yaml.Unmarshal([]byte(`
kind: hosted
name: agent
metadata:
  tags:
    - Test
    - Streaming
code_configuration:
  runtime: python_3_13
  entry_point: main.py
`), &definition))
	request, err := CreateAgentAPIRequestFromDefinition(definition)
	require.NoError(t, err)
	wire, err := json.Marshal(request)
	require.NoError(t, err)
	var decoded agent_api.CreateAgentRequest
	require.NoError(t, json.Unmarshal(wire, &decoded))
	assert.Equal(t, `["Streaming","Test"]`, decoded.Metadata["tags"])
	var tags []string
	require.NoError(t, json.Unmarshal([]byte(decoded.Metadata["tags"]), &tags))
	assert.Equal(t, []string{"Streaming", "Test"}, tags)
}
