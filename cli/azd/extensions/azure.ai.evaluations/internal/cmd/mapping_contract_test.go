// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

func TestMappingConversationDefaultsWithoutCatalogSchema(t *testing.T) {
	for _, mode := range []string{"static", "simulation", "traces", "responses"} {
		t.Run(mode, func(t *testing.T) {
			group := groupWith([]evalcore.EvaluatorRef{{Evaluator: "builtin.groundedness"}}, "conversation")
			group.Target = nil
			columns := map[string]bool{"messages": true}
			switch mode {
			case "simulation":
				group.Simulation = &project.Simulation{Model: "connection/simulator"}
				group.Target = &project.Target{Type: "agent", Name: "agent"}
				columns = map[string]bool{"test_case_description": true}
			case "traces", "responses":
				group.Source = &project.SourceDecl{Type: mode}
				group.Dataset = ""
				columns = nil
			}

			request, err := buildEvalRequest(group, nil, columns)
			require.NoError(t, err)
			require.Equal(t, "{{item.messages}}", request.TestingCriteria[0].DataMapping["messages"])
			require.NotContains(t, request.TestingCriteria[0].DataMapping, "query")
			require.NotContains(t, request.TestingCriteria[0].DataMapping, "response")
		})
	}
}

func TestMappingRejectsConflictingAndEmptyOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		mapping    map[string]string
	}{
		{"query and messages", exterrors.CodeConflictingArguments,
			map[string]string{"query": "{{item.query}}", "messages": "{{item.messages}}"}},
		{"response and messages", exterrors.CodeConflictingArguments,
			map[string]string{"response": "{{item.response}}", "messages": "{{item.messages}}"}},
		{"empty", exterrors.CodeInvalidParameter, map[string]string{"response": " "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := groupWith([]evalcore.EvaluatorRef{{Evaluator: "judge", DataMapping: tc.mapping}}, "turn")
			_, err := buildEvalRequest(group, nil, nil)
			require.Error(t, err)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, tc.code, local.Code)
			require.Contains(t, local.Message, "dataMapping")
			require.NotEmpty(t, local.Suggestion)
		})
	}
}

func TestMappingRequiredInteractionAlternative(t *testing.T) {
	for _, tc := range []struct {
		name      string
		columns   map[string]bool
		required  []string
		wantError string
	}{
		{"messages replaces response", map[string]bool{"messages": true}, []string{"response"}, ""},
		{"missing transcript", map[string]bool{}, []string{"response"}, "messages"},
		{"other required input", map[string]bool{"messages": true}, []string{"response", "context"}, "context"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contract := schema("judge", tc.required, []string{"query", "response", "messages", "context"},
				nil, nil, "turn", "conversation")
			group := groupWith([]evalcore.EvaluatorRef{{Evaluator: "judge"}}, "conversation")
			group.Target = nil
			request, err := buildEvalRequest(group, map[string]*eval_api.EvaluatorSummary{"judge": contract}, tc.columns)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, map[string]string{
				"messages": "{{item.messages}}", "tool_definitions": "{{item.tool_definitions}}",
			}, request.TestingCriteria[0].DataMapping)
		})
	}
}

func TestMappingExplicitResponseKeepsItsSource(t *testing.T) {
	group := groupWith([]evalcore.EvaluatorRef{{
		Evaluator: "judge", DataMapping: map[string]string{"response": "{{item.saved_answer}}"},
	}}, "turn")
	request, err := buildEvalRequest(group, nil, map[string]bool{"query": true, "saved_answer": true})
	require.NoError(t, err)
	require.Equal(t, "{{item.saved_answer}}", request.TestingCriteria[0].DataMapping["response"])
}

func TestMappingExplicitMessagesSuppressLegacyTurnDefaults(t *testing.T) {
	group := groupWith([]evalcore.EvaluatorRef{{
		Evaluator: "judge", DataMapping: map[string]string{"messages": "{{item.transcript}}"},
	}}, "turn")
	group.Target = nil
	contract := schema("judge", nil, []string{"query", "response"}, nil, nil, "turn")
	request, err := buildEvalRequest(group, map[string]*eval_api.EvaluatorSummary{"judge": contract},
		map[string]bool{"query": true, "response": true, "transcript": true})
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"messages": "{{item.transcript}}", "tool_definitions": "{{item.tool_definitions}}",
	}, request.TestingCriteria[0].DataMapping)
}

func TestMappingSharedColumnSatisfiesEveryCriterion(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		refs := []evalcore.EvaluatorRef{
			{Evaluator: "first", DataMapping: map[string]string{"response": "{{item.shared}}"}},
			{Evaluator: "second", DataMapping: map[string]string{"messages": "{{item.shared}}"}},
		}
		if reverse {
			refs[0], refs[1] = refs[1], refs[0]
		}
		group := groupWith(refs, "turn")
		group.Target = nil
		request, err := buildEvalRequest(group, nil, map[string]bool{"shared": true})
		require.NoError(t, err)
		compiler := jsonschema.NewCompiler()
		require.NoError(t, compiler.AddResource("https://fixture.test/shared.json", request.DataSourceConfig.ItemSchema))
		compiled, err := compiler.Compile("https://fixture.test/shared.json")
		require.NoError(t, err)
		require.NoError(t, compiled.Validate(map[string]any{
			"shared": []any{map[string]any{"role": "assistant", "content": "Answer."}},
		}))
		require.Error(t, compiled.Validate(map[string]any{"shared": "not a message array"}))
	}
}

func TestMappingExplicitInteractionOverridesDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, level string
		mapping     map[string]string
		columns     map[string]bool
	}{
		{
			"messages at turn level", "turn",
			map[string]string{"messages": "{{item.transcript}}"},
			map[string]bool{"query": true, "response": true, "transcript": true},
		},
		{
			"renamed turn fields", "turn",
			map[string]string{"query": "{{item.question}}", "response": "{{item.answer}}"},
			map[string]bool{"query": true, "response": true, "question": true, "answer": true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := evalcore.EvaluatorRef{Evaluator: "builtin.groundedness", DataMapping: tc.mapping}
			contract := schema(ref.Evaluator, nil, []string{"query", "response", "messages"},
				nil, nil, "turn", "conversation")
			group := groupWith([]evalcore.EvaluatorRef{ref}, tc.level)
			group.Target = nil
			request, err := buildEvalRequest(
				group, map[string]*eval_api.EvaluatorSummary{ref.Evaluator: contract}, tc.columns)
			require.NoError(t, err)
			want := map[string]string{"tool_definitions": "{{item.tool_definitions}}"}
			if _, messages := tc.mapping["messages"]; !messages {
				want["tool_calls"] = "{{item.tool_calls}}"
			}
			maps.Copy(want, tc.mapping)
			require.Equal(t, want, request.TestingCriteria[0].DataMapping)
			require.Equal(t, tc.mapping, ref.DataMapping, "do not mutate the authored mapping")
			properties, ok := request.DataSourceConfig.ItemSchema["properties"].(map[string]any)
			require.True(t, ok)
			require.NotContains(t, properties, "query", "discard superseded inferred columns")
			require.NotContains(t, properties, "response")
		})
	}
}

func TestMappingGeneratedResponseProvenance(t *testing.T) {
	for _, tc := range []struct {
		mode, responseType, want string
	}{
		{"model", "string", "{{sample.output_text}}"},
		{"agent", "string", "{{sample.output_text}}"},
		{"agent", "array", "{{sample.output_items}}"},
		{"responses", "string", "{{sample.output_text}}"},
		{"responses", "array", "{{sample.output_items}}"},
		{"static", "string", "{{item.response}}"},
	} {
		t.Run(tc.mode+"/"+tc.responseType, func(t *testing.T) {
			contract := schema("judge", []string{"response"}, []string{"query", "response"}, nil, nil, "turn")
			contract.Definition.DataSchema.Properties["response"] = map[string]any{"type": tc.responseType}
			group := groupWith([]evalcore.EvaluatorRef{{Evaluator: "judge"}}, "turn")
			switch tc.mode {
			case "static":
				group.Target = nil
			case "responses":
				group.Source = &project.SourceDecl{Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixture"}}
				group.Target = nil
				group.Dataset = ""
			default:
				group.Target.Type = tc.mode
			}
			request, err := buildEvalRequest(group, map[string]*eval_api.EvaluatorSummary{"judge": contract},
				map[string]bool{"query": true, "response": true})
			require.NoError(t, err)
			require.Equal(t, tc.want, request.TestingCriteria[0].DataMapping["response"],
				"generated output must not grade a stale dataset response")
		})
	}
}

func TestMappingExplicitStaticMessagesAtTurnLevel(t *testing.T) {
	contract := schema("builtin.groundedness", nil, []string{"query", "response", "messages"}, nil, nil, "turn")
	group := groupWith([]evalcore.EvaluatorRef{{
		Evaluator: contract.Name, DataMapping: map[string]string{"messages": "{{item.messages}}"},
	}}, "turn")
	group.Target = nil
	request, err := buildEvalRequest(group, map[string]*eval_api.EvaluatorSummary{contract.Name: contract},
		map[string]bool{"messages": true})
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"messages": "{{item.messages}}", "tool_definitions": "{{item.tool_definitions}}",
	}, request.TestingCriteria[0].DataMapping)
}

func TestMappingPreviewTurnTraceUsesCompletedItems(t *testing.T) {
	for _, withTarget := range []bool{false, true} {
		contract := schema("judge", []string{"response"}, []string{"query", "response"}, nil, nil, "turn")
		contract.Definition.DataSchema.Properties["response"] = map[string]any{"type": "string"}
		group := groupWith([]evalcore.EvaluatorRef{{Evaluator: "judge"}}, "turn")
		group.Source = &project.SourceDecl{Type: project.SourceTypeTraces, AgentName: "recorded"}
		if !withTarget {
			group.Target = nil
		}
		request, err := buildEvalRequest(group, map[string]*eval_api.EvaluatorSummary{"judge": contract}, nil)
		require.NoError(t, err)
		require.Equal(t, "{{item.response}}", request.TestingCriteria[0].DataMapping["response"],
			"a trace target filters existing interactions instead of invoking the target")
		require.False(t, request.DataSourceConfig.IncludeSampleSchema)
	}
}

func TestMappingGroundednessAcceptsActualStructuredRows(t *testing.T) {
	contract := schema("builtin.groundedness", []string{"response"}, []string{
		"query", "response", "context", "tool_definitions",
	}, nil, nil, "turn")
	group := groupWith([]evalcore.EvaluatorRef{{
		Evaluator: contract.Name, DataMapping: map[string]string{"context": "{{item.context}}"},
	}}, "turn")
	group.Target = nil
	request, err := buildEvalRequest(group, map[string]*eval_api.EvaluatorSummary{contract.Name: contract},
		map[string]bool{"query": true, "response": true, "context": true, "tool_definitions": true, "tool_calls": true})
	require.NoError(t, err)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(`[
		{"query":"Weather?","response":"Rainy.","context":"It is raining.","tool_definitions":[],"tool_calls":[]},
		{"query":[{"role":"user","content":"What is the weather?"}],
		 "response":[{"role":"tool","content":"Rainy."},{"role":"assistant","content":"It is raining."}],
		 "context":"Weather observation.","tool_definitions":[{"name":"weather"}],"tool_calls":[{"name":"weather"}]}
	]`), &rows))
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("https://fixture.test/item.json", request.DataSourceConfig.ItemSchema))
	compiled, err := compiler.Compile("https://fixture.test/item.json")
	require.NoError(t, err)
	for _, row := range rows {
		require.NoError(t, compiled.Validate(row))
		for field, mapping := range request.TestingCriteria[0].DataMapping {
			column, ok := itemColumn(mapping)
			require.True(t, ok)
			require.Equal(t, row[field], row[column], "the mapped input must retain its original value and type")
		}
	}
}
