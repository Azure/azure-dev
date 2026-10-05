// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The payload shapes follow the Microsoft Foundry target, conversation and RAG
// examples, not a recorded service response. This proves client binding and
// transport behavior, not hosted evaluator execution.
func TestMappingWireUsesSourceValues(t *testing.T) {
	const rowJSON = `{"query":"Weather?","response":"STALE dataset answer","context":"It is raining.",` +
		`"tool_calls":[],"tool_definitions":[]}`
	const conversationJSON = `{"messages":[{"role":"user","content":"Weather?"},` +
		`{"role":"assistant","content":"Rain."}],"tool_definitions":[]}`
	const seedJSON = `{"test_case_description":"Ask about the weather."}`
	for _, tc := range []struct {
		name, mode, level, rows, evaluator string
		want                               map[string]string
	}{
		{"static groundedness", "static", "turn", rowJSON, "builtin.groundedness", map[string]string{
			"query": "{{item.query}}", "response": "{{item.response}}", "context": "{{item.context}}",
			"tool_calls": "{{item.tool_calls}}", "tool_definitions": "{{item.tool_definitions}}",
		}},
		{"model groundedness", "model", "turn", rowJSON, "builtin.groundedness", map[string]string{
			"query": "{{item.query}}", "response": "{{sample.output_text}}", "context": "{{item.context}}",
			"tool_calls": "{{item.tool_calls}}", "tool_definitions": "{{item.tool_definitions}}",
		}},
		{"agent groundedness", "agent", "turn", rowJSON, "builtin.groundedness", map[string]string{
			"query": "{{item.query}}", "response": "{{sample.output_items}}", "context": "{{item.context}}",
			"tool_calls": "{{sample.tool_calls}}", "tool_definitions": "{{sample.tool_definitions}}",
		}},
		{"retrieved text", "responses", "turn", "", "builtin.violence", map[string]string{
			"query": "{{item.query}}", "response": "{{sample.output_text}}",
			"tool_calls": "{{sample.tool_calls}}", "tool_definitions": "{{sample.tool_definitions}}",
		}},
		{"static conversation", "static", "conversation", conversationJSON, "builtin.groundedness",
			map[string]string{"messages": "{{item.messages}}", "tool_definitions": "{{item.tool_definitions}}"}},
		{"static messages turn", "static", "turn", conversationJSON, "builtin.groundedness",
			map[string]string{"messages": "{{item.messages}}", "tool_definitions": "{{item.tool_definitions}}"}},
		{"simulation", "simulation", "conversation", seedJSON, "builtin.groundedness",
			map[string]string{"messages": "{{item.messages}}"}},
		{"traced conversation", "traces", "conversation", "", "builtin.task_completion",
			map[string]string{"messages": "{{item.messages}}", "tool_definitions": "{{item.tool_definitions}}"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan identityRequest, 20)
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				requests <- identityRequest{r.Method, r.URL.Path, body}
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/evaluators"):
					if r.URL.Query().Get("type") == eval_api.EvaluatorTypeBuiltin {
						_, _ = io.WriteString(w, `{"value":[
							{"name":"builtin.groundedness","supported_evaluation_levels":["turn","conversation"],
							 "definition":{"data_schema":{"type":"object","required":["response"],"properties":{
								"query":{"anyOf":[{"type":"string"},{"type":"array"}]},
								"response":{"anyOf":[{"type":"string"},{"type":"array"}]},
								"context":{"type":"string"},"messages":{"type":"array"}
							 }}}},
							{"name":"builtin.task_completion","supported_evaluation_levels":["turn","conversation"],
							 "definition":{"data_schema":{"properties":{
								"query":{"type":"array"},"response":{"type":"array"},"messages":{"type":"array"}
							 }}}},
							{"name":"builtin.violence","supported_evaluation_levels":["turn"],
							 "definition":{"data_schema":{"type":"object","required":["query","response"],
								"properties":{"query":{"type":"string"},"response":{"type":"string"}}}}}
						]}`)
					} else {
						_, _ = io.WriteString(w, `{"value":[]}`)
					}
				case strings.HasSuffix(r.URL.Path, "/runs"):
					assert.Equal(t, http.MethodPost, r.Method)
					_, _ = io.WriteString(w, `{"id":"evalrun_fixture","status":"queued"}`)
				case strings.HasSuffix(r.URL.Path, "/evals"):
					assert.Equal(t, http.MethodPost, r.Method)
					_, _ = io.WriteString(w, `{"id":"eval_fixture"}`)
				case strings.HasSuffix(r.URL.Path, "/versions"):
					_, _ = io.WriteString(w, `{"value":[{"name":"d","version":"1"}]}`)
				case strings.HasSuffix(r.URL.Path, "/credentials"):
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"sas_uri": srv.URL + "/rows.jsonl"}))
				case r.URL.Path == "/rows.jsonl":
					_, _ = io.WriteString(w, tc.rows+"\n")
				case strings.HasSuffix(r.URL.Path, "/versions/1"):
					_, _ = io.WriteString(w, `{"name":"d","version":"1","id":"registered-fixture-version"}`)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			ec := evalContextFor(srv)
			group := &project.Eval{
				Name: "mapping", Dataset: "d", EvaluationLevel: tc.level,
				Evaluators: []evalcore.EvaluatorRef{{Evaluator: tc.evaluator}},
			}
			if tc.level == "turn" && tc.rows == rowJSON {
				group.Evaluators[0].DataMapping = map[string]string{"context": "{{item.context}}"}
			}
			if tc.name == "static messages turn" {
				group.Evaluators[0].DataMapping = map[string]string{"messages": "{{item.messages}}"}
			}
			configPath := ""
			var columns map[string]bool
			var row map[string]any
			if tc.rows != "" {
				configPath = writeDataset(t, tc.rows+"\n")
				require.NoError(t, json.Unmarshal([]byte(tc.rows), &row))
				columns = map[string]bool{}
				for field := range row {
					columns[field] = true
				}
			}
			switch tc.mode {
			case "model", "agent":
				group.Target = &project.Target{Type: tc.mode, Name: "target"}
			case "simulation":
				group.Target = &project.Target{Type: "agent", Name: "target"}
				group.Simulation = &project.Simulation{Model: "connection/simulator"}
			case "traces":
				group.Dataset = ""
				group.Source = &project.SourceDecl{Type: project.SourceTypeTraces, AgentName: "recorded-agent"}
			case "responses":
				group.Dataset = ""
				group.Source = &project.SourceDecl{
					Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixture"},
				}
			}
			create, err := buildEvalRequest(group, ec.evaluatorSchemas(t.Context()), columns)
			require.NoError(t, err)
			eval, err := ec.evalClient.CreateOpenAIEval(t.Context(), create)
			require.NoError(t, err)
			source, _, err := ec.buildRunDataSource(t.Context(), group, configPath, 0)
			require.NoError(t, err)
			_, err = ec.evalClient.CreateOpenAIEvalRun(t.Context(), eval.ID, &eval_api.CreateOpenAIEvalRunRequest{
				Name: "mapping-run", EvaluationLevel: tc.level, DataSource: source,
			})
			require.NoError(t, err)

			var postedEval eval_api.CreateOpenAIEvalRequest
			var postedRun map[string]any
			downloaded := false
			for _, request := range recordedIdentityRequests(requests) {
				switch {
				case strings.HasSuffix(request.path, "/evals"):
					require.NoError(t, json.Unmarshal(request.body, &postedEval))
				case strings.HasSuffix(request.path, "/runs"):
					require.NoError(t, json.Unmarshal(request.body, &postedRun))
				case request.path == "/rows.jsonl":
					downloaded = true
				}
			}
			require.Len(t, postedEval.TestingCriteria, 1)
			require.Equal(t, tc.want, postedEval.TestingCriteria[0].DataMapping,
				"assert the HTTP payload, not just the plan")
			require.Equal(t, tc.mode == "model" || tc.mode == "agent",
				postedEval.DataSourceConfig.IncludeSampleSchema)
			if tc.mode == "responses" {
				require.Equal(t, "azure_ai_source", postedEval.DataSourceConfig.Type)
				require.Equal(t, "responses", postedEval.DataSourceConfig.Scenario)
			}
			require.Equal(t, tc.level, postedRun["evaluation_level"])
			runSource, ok := postedRun["data_source"].(map[string]any)
			require.True(t, ok)
			if tc.rows != "" {
				require.True(t, downloaded, "read the registered source content, not only its schema")
				require.Equal(t, map[string]any{
					"type": "file_id", "id": "registered-fixture-version",
				}, runSource["source"], "keep registered dataset identity")
			}

			// Resolve the captured mappings against distinct input and output
			// values so accidentally grading a seed or stale response cannot pass.
			sample := map[string]any{
				"output_text": "Generated answer",
				"tool_calls":  []any{}, "tool_definitions": []any{},
				"output_items": []any{
					map[string]any{"role": "tool", "content": "It is raining."},
					map[string]any{"role": "assistant", "content": "Generated answer"},
				},
			}
			if tc.mode == "responses" {
				row = map[string]any{"query": "Retrieved question", "response": "WRONG item response"}
			}
			if tc.mode == "simulation" || tc.mode == "traces" {
				require.NoError(t, json.Unmarshal([]byte(conversationJSON), &row))
			}
			for field, mapping := range postedEval.TestingCriteria[0].DataMapping {
				path := strings.TrimSuffix(strings.TrimPrefix(mapping, "{{"), "}}")
				namespace, column, ok := strings.Cut(path, ".")
				require.True(t, ok)
				values := row
				if namespace == "sample" {
					values = sample
				} else {
					require.Equal(t, "item", namespace)
				}
				value, present := values[column]
				require.True(t, present, "missing actual evaluator input %s: %s", field, mapping)
				require.NotNil(t, value)
				if field == "response" && tc.mode != "static" {
					require.NotEqual(t, "STALE dataset answer", value)
					require.NotEqual(t, "WRONG item response", value)
				}
				if field == "context" {
					require.Equal(t, "It is raining.", value)
				}
			}
		})
	}
}
