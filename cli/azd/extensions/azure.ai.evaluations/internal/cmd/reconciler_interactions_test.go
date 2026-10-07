// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatasetInteractionValidationUsesFinalMappings(t *testing.T) {
	turn := map[string]string{
		"query": "{{item.query}}", "response": "{{item.response}}",
		"tool_calls": "{{item.tool_calls}}", "tool_definitions": "{{item.tool_definitions}}",
	}
	conversation := map[string]string{
		"messages": "{{item.messages}}", "tool_definitions": "{{item.tool_definitions}}",
	}
	for _, tc := range []struct {
		name    string
		mapping map[string]string
		columns map[string]bool
		group   project.Eval
		missing string
	}{
		{"turn missing query", turn, map[string]bool{"response": true}, project.Eval{}, `"query"`},
		{"turn missing response", turn, map[string]bool{"query": true}, project.Eval{}, `"response"`},
		{"turn optional tools", turn, map[string]bool{"query": true, "response": true}, project.Eval{}, ""},
		{"conversation missing messages", conversation, map[string]bool{"query": true}, project.Eval{}, `"messages"`},
		{"conversation optional tools", conversation, map[string]bool{"messages": true}, project.Eval{}, ""},
		{
			"messages format at turn scoring", conversation, map[string]bool{"messages": true},
			project.Eval{EvaluationLevel: project.EvaluationLevelTurn}, "",
		},
		{
			"explicit transcript column", map[string]string{"messages": "{{item.transcript}}"},
			map[string]bool{"messages": true}, project.Eval{}, `"transcript"`,
		},
		{
			"explicit transcript supplied", map[string]string{"messages": "{{item.transcript}}"},
			map[string]bool{"transcript": true}, project.Eval{}, "",
		},
		{
			"generated response", map[string]string{"query": "{{item.query}}", "response": "{{sample.output_items}}"},
			map[string]bool{"query": true}, project.Eval{}, "",
		},
		{
			"simulation generates messages", conversation, map[string]bool{"test_case_description": true},
			project.Eval{Simulation: &project.Simulation{}}, "",
		},
		{"trace source", turn, nil, project.Eval{Source: &project.SourceDecl{Type: project.SourceTypeTraces}}, ""},
		{
			"response source", conversation, nil,
			project.Eval{Source: &project.SourceDecl{Type: project.SourceTypeResponses}}, "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := maps.Clone(tc.mapping)
			request := &eval_api.CreateOpenAIEvalRequest{TestingCriteria: []eval_api.TestingCriterion{{
				EvaluatorName: "custom", DataMapping: tc.mapping,
			}}}
			err := validateDatasetInteractions(&tc.group, request, tc.columns, nil)
			if tc.missing == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.missing)
				assert.Contains(t, err.Error(), "data_mapping")
			}
			assert.Equal(t, before, request.TestingCriteria[0].DataMapping,
				"validation must not remove exact optional default mappings")
		})
	}
}

func TestDatasetInteractionValidationChecksEveryCriterion(t *testing.T) {
	request := &eval_api.CreateOpenAIEvalRequest{TestingCriteria: []eval_api.TestingCriterion{
		{EvaluatorName: "first", DataMapping: map[string]string{"messages": "{{item.messages}}"}},
		{EvaluatorName: "second", DataMapping: map[string]string{
			"query": "{{item.missing}}", "response": "{{item.missing}}",
		}},
	}}
	err := validateDatasetInteractions(&project.Eval{}, request, map[string]bool{"messages": true}, nil)
	require.ErrorContains(t, err, `"second"`)
	assert.Equal(t, 1, strings.Count(err.Error(), `"missing"`), "one missing column needs one diagnostic")
}

func TestDatasetInteractionValidationRequiresInputsOnEveryRow(t *testing.T) {
	columns, err := inspectJSONLContent(t.Context(), "local rows", strings.NewReader(
		"{\"query\":\"first\",\"response\":\"answer\"}\n{\"query\":\"second\"}\n"), nil)
	require.NoError(t, err)
	request := &eval_api.CreateOpenAIEvalRequest{TestingCriteria: []eval_api.TestingCriterion{{
		EvaluatorName: "custom", DataMapping: map[string]string{
			"query": "{{item.query}}", "response": "{{item.response}}",
		},
	}}}
	require.ErrorContains(t, validateDatasetInteractions(&project.Eval{}, request, columns, nil), `"response"`)
}

func TestMappedDatasetInteractionsFailBeforePublication(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, tc := range []struct {
			name    string
			rows    string
			mapping map[string]string
			missing string
		}{
			{
				"missing query", `{"response":"answer"}`,
				map[string]string{"query": "{{item.query}}", "response": "{{item.response}}"}, `"query"`,
			},
			{
				"missing response", `{"query":"question"}`,
				map[string]string{"query": "{{item.query}}", "response": "{{item.response}}"}, `"response"`,
			},
			{
				"missing transcript", `{"query":"question","response":"answer"}`,
				map[string]string{"messages": "{{item.transcript}}"}, `"transcript"`,
			},
			{
				"empty query", `{"query":"","response":"answer"}`,
				map[string]string{"query": "{{item.query}}", "response": "{{item.response}}"}, `"query"`,
			},
			{
				"numeric query", `{"query":42,"response":"answer"}`,
				map[string]string{"query": "{{item.query}}", "response": "{{item.response}}"}, `"query"`,
			},
			{
				"null query", `{"query":null,"response":"answer"}`,
				map[string]string{"query": "{{item.query}}", "response": "{{item.response}}"}, `"query"`,
			},
		} {
			t.Run(caller+"/"+tc.name, func(t *testing.T) {
				ec, env, service, cfg, dir := validationFixture(t)
				service.definition = `{"definition":{"data_schema":{"properties":{}}}}`
				cfg.Evals[0].Evaluators[0].DataMapping = tc.mapping
				require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(tc.rows), 0o600))
				require.ErrorContains(t, reconcileArtifactConfig(t, caller, ec, cfg, dir), tc.missing)
				for _, request := range service.requests {
					assert.True(t, strings.HasPrefix(request, "GET "), "unexpected publication: %s", request)
				}
				assert.Zero(t, service.createCount)
				assert.False(t, service.dataset)
				assert.Empty(t, env.config)
				assert.Empty(t, env.values)
			})
		}
	}
}

func TestDatasetInteractionValidationRejectsMalformedValues(t *testing.T) {
	for _, field := range []string{"query", "response", "messages"} {
		t.Run(field, func(t *testing.T) {
			mapping := map[string]string{"query": "{{item.query}}", "response": "{{item.response}}"}
			columns := map[string]bool{"query": true, "response": true}
			if field == "messages" {
				mapping = map[string]string{"messages": "{{item.messages}}"}
				columns = map[string]bool{"messages": true}
			}
			request := &eval_api.CreateOpenAIEvalRequest{TestingCriteria: []eval_api.TestingCriterion{{
				EvaluatorName: "custom", DataMapping: mapping,
			}}}
			err := validateDatasetInteractions(&project.Eval{}, request, columns, map[string]bool{field: true})
			require.ErrorContains(t, err, `"`+field+`"`)
			assert.Contains(t, err.Error(), "non-empty")
			assert.Contains(t, err.Error(), "data_mapping")
		})
	}
}

func TestDatasetInteractionValidationPrefersMissingOverMalformed(t *testing.T) {
	request := &eval_api.CreateOpenAIEvalRequest{TestingCriteria: []eval_api.TestingCriterion{{
		EvaluatorName: "custom", DataMapping: map[string]string{
			"query": "{{item.query}}", "response": "{{item.response}}",
		},
	}}}
	err := validateDatasetInteractions(
		&project.Eval{}, request, map[string]bool{"response": true}, map[string]bool{"response": true})
	require.ErrorContains(t, err, `"query"`)
	assert.NotContains(t, err.Error(), "non-empty")
}

func TestMalformedTextValue(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     any
		malformed bool
	}{
		{"non-empty string", "hello", false},
		{"non-empty array", []any{map[string]any{"role": "user", "content": "hi"}}, false},
		{"string array", []any{"not-a-message"}, true},
		{"number array", []any{float64(1)}, true},
		{"mixed array", []any{map[string]any{"role": "user"}, "not-a-message"}, true},
		{"whitespace string", " \t\n", true},
		{"empty string", "", true},
		{"empty array", []any{}, false},
		{"number", float64(42), true},
		{"bool", true, true},
		{"null", nil, true},
		{"object", map[string]any{"a": "b"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.malformed, malformedTextValue(tc.value))
		})
	}
}

func TestDatasetInteractionValidationAcceptsMessageArrayQuery(t *testing.T) {
	ec, _, service, cfg, dir := validationFixture(t)
	rows := `{"query":[{"role":"user","content":"hi"}],"response":[{"role":"assistant","content":"hello"}]}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(rows), 0o600))
	require.NoError(t, reconcileArtifactConfig(t, "create", ec, cfg, dir))
	assert.Equal(t, 1, service.createCount)
}

func TestDatasetInteractionValidationRejectsMixedStaticRows(t *testing.T) {
	ec, env, service, cfg, dir := validationFixture(t)
	rows := `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]}` + "\n" +
		`{"query":"where is my order?","response":"on the way"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(rows), 0o600))
	cfg.Evals[0].EvaluationLevel = "conversation"
	service.definition = `{"name":"builtin.valid","version":"1","definition":{"data_schema":` +
		`{"properties":{"messages":{"type":"array"}},"required":["messages"]},` +
		`"supported_evaluation_levels":["conversation"]}}`
	require.ErrorContains(t, reconcileArtifactConfig(t, "create", ec, cfg, dir), `"messages"`)
	assert.Zero(t, service.createCount)
	assert.Empty(t, env.config)
}

func TestMalformedDatasetInteractionPersistsAcrossRows(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, _, service, cfg, dir := validationFixture(t)
			rows := "{\"query\":\" \",\"response\":\"answer\"}\n{\"query\":\"valid\",\"response\":\"answer\"}\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(rows), 0o600))
			require.ErrorContains(t, reconcileArtifactConfig(t, caller, ec, cfg, dir), `"query"`)
			assert.Zero(t, service.createCount)
			assert.False(t, service.dataset)
		})
	}
}
