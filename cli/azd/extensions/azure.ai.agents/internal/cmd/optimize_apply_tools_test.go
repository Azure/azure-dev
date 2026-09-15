// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestPersistPromptAgentCandidateConfigFunctionTools(t *testing.T) {
	t.Parallel()

	const existing = `[
		{"type":"web_search","name":"search","search_context_size":"low"},
		{"type":"function","name":"lookup_travel_policy","description":"${TOOL_DESCRIPTION}","strict":true,
		 "parameters":{"type":"object","properties":{
			"destination":{"type":"string","description":"Original destination.","enum":["US","CA"]},
			"reason":{"type":"string","description":"Keep this."}
		 },"required":["destination"],"additionalProperties":false}},
		{"type":"function","name":"unchanged","description":"Keep this function.","parameters":{}}
	]`
	const merged = `[
		{"type":"web_search","name":"search","search_context_size":"low"},
		{"type":"function","name":"lookup_travel_policy","description":"Optimized description.","strict":true,
		 "parameters":{"type":"object","properties":{
			"destination":{"type":"string","description":"Optimized destination.","enum":["US","CA"]},
			"reason":{"type":"string","description":"Keep this."}
		 },"required":["destination"],"additionalProperties":false}},
		{"type":"function","name":"unchanged","description":"Keep this function.","parameters":{}}
	]`
	tests := []struct {
		name      string
		candidate string
		expected  string
	}{
		{
			name: "flat functions only with exact matching names",
			candidate: `[
				{"type":"function","name":"new_function","description":"Do not add."},
				{"type":"function","name":"lookup_travel_policy","description":"Optimized description.",
				 "parameters":{"properties":{"destination":{"description":"Optimized destination."}}}},
				{"type":"function","name":"search","description":"Do not replace a non-function."},
				{"type":"web_search","name":"search","search_context_size":"high"}
			]`,
			expected: merged,
		},
		{
			name: "nested function saved flat",
			candidate: `[{"type":"function","function":{
				"name":"lookup_travel_policy","description":"Optimized description.",
				"parameters":{"properties":{"destination":{"description":"Optimized destination."}}}
			}}]`,
			expected: merged,
		},
		{
			name:      "name only preserves every omitted field",
			candidate: `[{"type":"function","name":"lookup_travel_policy"}]`,
			expected:  existing,
		},
		{
			name:      "empty parameter object preserves omitted schema fields",
			candidate: `[{"type":"function","name":"lookup_travel_policy","parameters":{}}]`,
			expected:  existing,
		},
		{
			name: "nonmatching names and non-function tools ignored",
			candidate: `[
				{"type":"function","name":"LOOKUP_TRAVEL_POLICY","description":"Wrong case."},
				{"type":"function","name":"search","description":"Not an existing function."},
				{"type":"web_search","name":"lookup_travel_policy"},
				{"type":"future_tool"}
			]`,
			expected: existing,
		},
		{
			name: "multiple matches preserve existing order",
			candidate: `[
				{"type":"function","name":"unchanged","description":"Also updated."},
				{"type":"function","name":"lookup_travel_policy","description":"Updated."}
			]`,
			expected: `[
				{"type":"web_search","name":"search","search_context_size":"low"},
				{"type":"function","name":"lookup_travel_policy","description":"Updated.","strict":true,
				 "parameters":{"type":"object","properties":{
					"destination":{"type":"string","description":"Original destination.","enum":["US","CA"]},
					"reason":{"type":"string","description":"Keep this."}
				 },"required":["destination"],"additionalProperties":false}},
				{"type":"function","name":"unchanged","description":"Also updated.","parameters":{}}
			]`,
		},
		{
			name: "explicit null optional fields are not omissions",
			candidate: `[{"type":"function","name":"lookup_travel_policy",
				"description":null,"strict":null,"parameters":null}]`,
			expected: `[
				{"type":"web_search","name":"search","search_context_size":"low"},
				{"type":"function","name":"lookup_travel_policy","description":null,"strict":null,"parameters":null},
				{"type":"function","name":"unchanged","description":"Keep this function.","parameters":{}}
			]`,
		},
		{
			name: "explicit empty strings false and arrays replace existing values",
			candidate: `[{"type":"function","name":"lookup_travel_policy","description":"","strict":false,
				"parameters":{"required":[],"properties":{"destination":{"enum":["GB"]}}}}]`,
			expected: `[
				{"type":"web_search","name":"search","search_context_size":"low"},
				{"type":"function","name":"lookup_travel_policy","description":"","strict":false,
				 "parameters":{"type":"object","properties":{
					"destination":{"type":"string","description":"Original destination.","enum":["GB"]},
					"reason":{"type":"string","description":"Keep this."}
				 },"required":[],"additionalProperties":false}},
				{"type":"function","name":"unchanged","description":"Keep this function.","parameters":{}}
			]`,
		},
	}
	for _, legacy := range []bool{false, true} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("legacy=%t/%s", legacy, tt.name), func(t *testing.T) {
				t.Parallel()
				svc := newPromptCandidateTestService(t, legacy)
				server, path := newPromptCandidateTestServer(t, svc, legacy)
				before := server.rawSections[svc.Name][path].AsMap()
				var existingTools []any
				require.NoError(t, json.Unmarshal([]byte(existing), &existingTools))
				before["tools"] = existingTools
				var err error
				server.rawSections[svc.Name][path], err = structpb.NewStruct(before)
				require.NoError(t, err)
				root := t.TempDir()
				writePromptCandidateTestProject(t, root, svc.Name, path, before)
				expected := server.rawSections[svc.Name][path].AsMap()
				var expectedTools []any
				require.NoError(t, json.Unmarshal([]byte(tt.expected), &expectedTools))
				expected["tools"] = expectedTools
				expected["model"] = "gpt-5"
				expected["instructions"] = "Optimized instructions."
				client := newProjectRecorderClient(t, server)
				candidate := json.RawMessage(`{"model":"gpt-5","instructions":"Optimized instructions.","tools":` +
					tt.candidate + `}`)

				require.NoError(t, persistPromptAgentCandidateConfig(t.Context(), client, svc, root, candidate))

				server.mu.Lock()
				defer server.mu.Unlock()
				require.Empty(t, server.configSectionReads)
				require.Len(t, server.configSections, 1)
				require.Equal(t, path, server.configSections[0].Path)
				require.Equal(t, expected, server.configSections[0].Section.AsMap())
				require.Empty(t, server.configValues)
				require.Empty(t, server.unsetPaths)
			})
		}
	}
}

func TestPersistPromptAgentCandidateConfigPreservesAbsentTools(t *testing.T) {
	t.Parallel()

	for _, legacy := range []bool{false, true} {
		for _, state := range []string{"missing", "null", "empty"} {
			t.Run(fmt.Sprintf("legacy=%t/%s", legacy, state), func(t *testing.T) {
				t.Parallel()
				svc := newPromptCandidateTestService(t, legacy)
				server, path := newPromptCandidateTestServer(t, svc, legacy)
				expected := server.rawSections[svc.Name][path].AsMap()
				switch state {
				case "missing":
					delete(expected, "tools")
				case "null":
					expected["tools"] = nil
				case "empty":
					expected["tools"] = []any{}
				}
				var err error
				server.rawSections[svc.Name][path], err = structpb.NewStruct(expected)
				require.NoError(t, err)
				root := t.TempDir()
				writePromptCandidateTestProject(t, root, svc.Name, path, expected)
				client := newProjectRecorderClient(t, server)
				candidate := json.RawMessage(`{"model":"gpt-5","instructions":"Optimized instructions.",
					"tools":[{"type":"function","name":"lookup_travel_policy","description":"Do not add."}]}`)

				require.NoError(t, persistPromptAgentCandidateConfig(t.Context(), client, svc, root, candidate))

				expected["model"] = "gpt-5"
				expected["instructions"] = "Optimized instructions."
				server.mu.Lock()
				defer server.mu.Unlock()
				require.Empty(t, server.configSectionReads)
				require.Len(t, server.configSections, 1)
				require.Equal(t, expected, server.configSections[0].Section.AsMap())
			})
		}
	}
}

func TestPersistPromptAgentCandidateConfigRejectsInvalidFunctionTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		tools string
		err   string
	}{
		{"null entry", `[null]`, "must be an object"},
		{"string entry", `["function"]`, "must be an object"},
		{"missing type", `[{}]`, "type must be a non-empty string"},
		{"invalid type", `[{"type":1}]`, "type must be a non-empty string"},
		{"empty type", `[{"type":" "}]`, "type must be a non-empty string"},
		{"null function", `[{"type":"function","function":null}]`, "function must be an object"},
		{"array function", `[{"type":"function","function":[]}]`, "function must be an object"},
		{"missing name", `[{"type":"function"}]`, "name must be a non-empty string"},
		{"invalid name", `[{"type":"function","name":1}]`, "name must be a non-empty string"},
		{"empty name", `[{"type":"function","function":{"name":" "}}]`, "name must be a non-empty string"},
		{"invalid description", `[{"type":"function","name":"f","description":1}]`, "description must be a string"},
		{"invalid parameters", `[{"type":"function","name":"f","parameters":[]}]`, "parameters must be an object"},
		{"invalid strict", `[{"type":"function","name":"f","strict":"true"}]`, "strict must be a boolean"},
		{"mixed formats", `[{"type":"function","name":"f","function":{"name":"f"}}]`, "mixes flat and nested"},
		{"mixed descriptions", `[{"type":"function","description":"x","function":{"name":"f"}}]`,
			"mixes flat and nested"},
		{"duplicate names", `[{"type":"function","name":"f"},{"type":"function","function":{"name":"f"}}]`,
			"duplicate function name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := &recordingProjectServer{}
			client := newProjectRecorderClient(t, server)
			candidate := json.RawMessage(`{"model":"gpt-5","instructions":"Keep this.","tools":` + tt.tools + `}`)
			err := persistPromptAgentCandidateConfig(
				t.Context(), client, newPromptCandidateTestService(t, false), t.TempDir(), candidate,
			)
			require.ErrorContains(t, err, tt.err)
			server.mu.Lock()
			defer server.mu.Unlock()
			require.Empty(t, server.configSectionReads)
			require.Empty(t, server.configSections)
			require.Empty(t, server.configValues)
			require.Empty(t, server.unsetPaths)
		})
	}
}

func TestPersistPromptAgentCandidateConfigRejectsNonArrayExistingTools(t *testing.T) {
	t.Parallel()
	svc := newPromptCandidateTestService(t, false)
	server, path := newPromptCandidateTestServer(t, svc, false)
	before := server.rawSections[svc.Name][path].AsMap()
	before["tools"] = "${TOOLS}"
	var err error
	server.rawSections[svc.Name][path], err = structpb.NewStruct(before)
	require.NoError(t, err)
	root := t.TempDir()
	writePromptCandidateTestProject(t, root, svc.Name, path, before)
	client := newProjectRecorderClient(t, server)
	candidate := json.RawMessage(`{"model":"gpt-5","instructions":"Optimized instructions.",
		"tools":[{"type":"function","name":"lookup_travel_policy","description":"Updated."}]}`)

	err = persistPromptAgentCandidateConfig(t.Context(), client, svc, root, candidate)

	require.ErrorContains(t, err, "existing tools must be an array")
	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.configSectionReads)
	require.Empty(t, server.configSections)
	require.Equal(t, before, server.rawSections[svc.Name][path].AsMap())
}
