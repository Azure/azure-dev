// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func responseGroup() project.Eval {
	return project.Eval{
		Name: "stored", EvaluationLevel: "conversation",
		Source: &project.SourceDecl{Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixed"}, MaxTurns: 1},
		Evaluators: evalcore.EvaluatorList{
			{Evaluator: "builtin.coherence"}, {Evaluator: "builtin.task_completion"},
		},
	}
}

func TestBuildResponseScenarioLeavesOtherModesCustom(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{}
	for _, name := range []string{"builtin.coherence", "builtin.task_completion"} {
		schemas[name] = schema(name, nil, []string{"messages", "query", "response"}, nil, nil, "conversation", "turn")
	}
	for _, mode := range []string{"responses", "traces", "static", "turn", "simulation"} {
		t.Run(mode, func(t *testing.T) {
			group := responseGroup()
			switch mode {
			case "traces":
				group.Source = &project.SourceDecl{Type: project.SourceTypeTraces, AgentName: "agent"}
			case "static":
				group.Source = nil
				group.Dataset = "d"
			case "turn":
				group.Source = nil
				group.Dataset = "d"
				group.Target = &project.Target{Type: "agent", Name: "agent"}
				group.EvaluationLevel = "turn"
			case "simulation":
				group.Source = nil
				group.Dataset = "d"
				group.Target = &project.Target{Type: "agent", Name: "agent"}
				group.Simulation = &project.Simulation{Model: "model", NumConversations: 1}
			}
			req, err := buildEvalRequest(&group, schemas, nil)
			require.NoError(t, err)
			raw, err := json.Marshal(req.DataSourceConfig)
			require.NoError(t, err)
			if mode == "responses" {
				assert.JSONEq(t, `{"type":"azure_ai_source","scenario":"responses"}`, string(raw))
				for _, criterion := range req.TestingCriteria {
					assert.Equal(t, map[string]string{"messages": "{{item.messages}}"}, criterion.DataMapping)
				}
			} else {
				assert.Equal(t, "custom", req.DataSourceConfig.Type)
				assert.Empty(t, req.DataSourceConfig.Scenario)
				assert.Contains(t, string(raw), `"item_schema":`)
				assert.Contains(t, string(raw), `"include_sample_schema":`)
			}
		})
	}
}

func TestResponseSchemaMigrationIsPersistentAndIdempotent(t *testing.T) {
	for _, renamed := range []bool{false, true} {
		t.Run(map[bool]string{false: "cached", true: "renamed"}[renamed], func(t *testing.T) {
			group := responseGroup()
			digest, err := project.FingerprintGroup(group)
			require.NoError(t, err)
			definition, err := project.FingerprintDefinition(group)
			require.NoError(t, err)
			state := map[string]string{
				project.FingerprintKey("eval", group.Name): fingerprintEra + definition,
				digestIDKey(digest):                        "eval_old", "unrelated": "keep",
			}
			if !renamed {
				state[idKey("eval", group.Name)] = "eval_old"
			}
			env := &testEnvServer{state: state}
			created := 0
			var request map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/eval_old"):
					_, _ = io.WriteString(w, `{"id":"eval_old","name":"old-name","data_source_config":{"type":"custom"}}`)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/evals"):
					created++
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					_, _ = io.WriteString(w, `{"id":"eval_new"}`)
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/eval_new"):
					_, _ = io.WriteString(w, `{"id":"eval_new","name":"stored","data_source_config":{
						"type":"azure_ai_source","scenario":"responses","service_added":true}}`)
				default:
					t.Errorf("unexpected mutation or request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(srv.Close)
			for attempt := range 3 {
				ec := evalContextFor(srv)
				ec.azdClient, ec.envName = newTestAzdClient(t, env), "test"
				ec.schemas = map[string]*eval_api.EvaluatorSummary{}
				reconciler := &evalReconciler{ec: ec}
				reconciler.ReserveDeclared(t.Context(), []project.Eval{group})
				id, changed, err := reconciler.EnsureEval(t.Context(), group, "")
				require.NoError(t, err)
				require.Equal(t, "eval_new", id)
				require.Equal(t, attempt == 0, changed)
			}
			require.Equal(t, 1, created)
			assert.Equal(t,
				map[string]any{"type": "azure_ai_source", "scenario": "responses"}, request["data_source_config"])
			assert.Equal(t, "eval_new", env.stored(t, idKey("eval", group.Name)))
			assert.Equal(t, "eval_new", env.stored(t, digestIDKey(digest)))
			assert.Equal(t, "keep", env.stored(t, "unrelated"))
		})
	}
}

func TestExplicitResponseEvalRequiresCompatibleSchema(t *testing.T) {
	for _, config := range []map[string]any{
		nil, {"type": "custom"}, {"type": "azure_ai_source", "scenario": "traces_preview"},
		{"type": "azure_ai_source", "scenario": "responses"},
	} {
		held := eval_api.OpenAIEval{ID: "eval_fixed", DataSourceConfig: config}
		r, seen := reconcilerHoldingEval(t, held)
		group := responseGroup()
		group.ID = held.ID
		id, changed, err := r.EnsureEval(t.Context(), group, "")
		if hasResponsesSchema(&held) {
			require.NoError(t, err)
			assert.Equal(t, held.ID, id)
		} else {
			require.ErrorContains(t, err, "stored-responses schema")
			assert.Empty(t, id)
		}
		assert.False(t, changed)
		assert.Nil(t, seen.body)
	}
}

func TestResponseSchemaValidationRejectsExplicitIDBeforePublication(t *testing.T) {
	group := responseGroup()
	group.ID = "eval_old"
	r, seen := reconcilerHoldingEval(t, eval_api.OpenAIEval{
		ID: group.ID, DataSourceConfig: map[string]any{"type": "custom"},
	})
	err := r.Validate(t.Context(), &project.EvalConfig{Evals: []project.Eval{group}}, t.TempDir())
	require.ErrorContains(t, err, "stored-responses schema")
	require.Nil(t, seen.body)
}

func TestResponseMigrationFailuresPreserveOldIdentity(t *testing.T) {
	for _, failure := range []string{"read", "create"} {
		t.Run(failure, func(t *testing.T) {
			group := responseGroup()
			creates := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/eval_old"):
					if failure == "read" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					_, _ = io.WriteString(w, `{"id":"eval_old","data_source_config":{"type":"custom"}}`)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/evals"):
					creates++
					w.WriteHeader(http.StatusServiceUnavailable)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(srv.Close)
			ec := evalContextFor(srv)
			ec.schemas = map[string]*eval_api.EvaluatorSummary{}
			ec.state = map[string]string{idKey("eval", group.Name): "eval_old", "unrelated": "keep"}
			id, changed, err := (&evalReconciler{ec: ec}).EnsureEval(t.Context(), group, "")
			require.Error(t, err)
			assert.Empty(t, id)
			assert.False(t, changed)
			assert.Equal(t, "eval_old", ec.state[idKey("eval", group.Name)])
			assert.Equal(t, "keep", ec.state["unrelated"])
			if failure == "read" {
				assert.Zero(t, creates)
			} else {
				assert.Equal(t, 1, creates)
			}
		})
	}
}

func TestResponseMigrationDoesNotSplitOtherCustomHistories(t *testing.T) {
	for _, mode := range []string{"traces", "static", "turn", "simulation"} {
		t.Run(mode, func(t *testing.T) {
			group := project.Eval{Name: mode, Dataset: "d"}
			switch mode {
			case "traces":
				group.Dataset = ""
				group.Source = &project.SourceDecl{Type: project.SourceTypeTraces, AgentName: "agent"}
			case "turn":
				group.Target = &project.Target{Name: "agent"}
			case "simulation":
				group.Target = &project.Target{Name: "agent"}
				group.Simulation = &project.Simulation{Model: "model", NumConversations: 1}
				group.EvaluationLevel = "conversation"
			}
			remote := eval_api.OpenAIEval{ID: "eval_custom", Name: mode, DataSourceConfig: map[string]any{"type": "custom"}}
			r, seen := reconcilerHoldingEval(t, remote)
			r.ec.schemas = map[string]*eval_api.EvaluatorSummary{}
			r.ec.state = map[string]string{idKey("eval", mode): remote.ID}
			id, changed, err := r.EnsureEval(t.Context(), group, "")
			require.NoError(t, err)
			assert.Equal(t, remote.ID, id)
			assert.False(t, changed)
			assert.Nil(t, seen.body)
			assert.False(t, responseSchemaMatches(&group, &eval_api.OpenAIEval{
				DataSourceConfig: map[string]any{"type": "azure_ai_source", "scenario": "responses"},
			}), "switching away from responses must restore a custom schema")
		})
	}
}

func TestResponseRunCallerPreservesFixedIDsAndRejectsLegacySources(t *testing.T) {
	for _, mode := range []string{"valid", "custom eval", "bare rows", "missing params", "read failure"} {
		t.Run(mode, func(t *testing.T) {
			source := eval_api.NewResponsesDataSource([]string{"resp_fixed"}, 1)
			if mode == "bare rows" {
				source.ItemGenerationParams.Source.Content = []map[string]any{{"response_id": "resp_fixed"}}
			}
			if mode == "missing params" {
				source.ItemGenerationParams = nil
			}
			original, err := json.Marshal(source)
			require.NoError(t, err)
			posts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/runs"):
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": []any{
						map[string]any{"id": "previous", "data_source": source, "evaluation_level": "conversation"},
					}}))
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/eval_fixed"):
					if mode == "read failure" {
						w.WriteHeader(http.StatusForbidden)
					} else if mode == "custom eval" {
						_, _ = io.WriteString(w, `{"id":"eval_fixed","data_source_config":{"type":"custom"}}`)
					} else {
						_, _ = io.WriteString(w, `{"id":"eval_fixed","data_source_config":{
							"type":"azure_ai_source","scenario":"responses"}}`)
					}
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/eval_fixed/runs"):
					posts++
					var request eval_api.CreateOpenAIEvalRunRequest
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					assert.Equal(t, source, request.DataSource)
					assert.Equal(t, "conversation", request.EvaluationLevel)
					_, _ = io.WriteString(w, `{"id":"run_fixed","status":"queued"}`)
				default:
					t.Errorf("unexpected request (must not invoke an agent): %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(srv.Close)
			var out bytes.Buffer
			command := &cobra.Command{}
			command.SetOut(&out)
			command.Flags().String("output", "json", "")
			action := &runStartAction{cmd: command, flags: &runStartFlags{
				groupName: "eval_fixed", evalPath: t.TempDir(),
			}}
			err = action.start(t.Context(), evalContextFor(srv), gate{})
			if mode == "valid" {
				require.NoError(t, err)
				require.Equal(t, 1, posts)
				var result map[string]any
				require.NoError(t, json.Unmarshal(out.Bytes(), &result))
				assert.Equal(t, "run_fixed", result["run_id"])
				assert.Equal(t, "eval_fixed", result["eval_id"])
				assert.NotContains(t, result, "data_source")
			} else {
				require.Error(t, err)
				require.Zero(t, posts)
				require.Empty(t, out.String())
			}
			after, err := json.Marshal(source)
			require.NoError(t, err)
			assert.JSONEq(t, string(original), string(after), "previous run history must not be rewritten")
		})
	}
}

func TestResponseBuilderRejectsEmptyIDsAndCaps(t *testing.T) {
	for _, ids := range [][]string{nil, {}, {""}, {" \t"}, {"resp_fixed", ""}} {
		group := responseGroup()
		group.Source.ResponseIDs = ids
		_, _, err := (&evalContext{}).buildRunDataSource(t.Context(), &group, "", 0)
		require.ErrorContains(t, err, "source.response_ids")
	}
	group := responseGroup()
	_, _, err := (&evalContext{}).buildRunDataSource(t.Context(), &group, "", 1)
	require.ErrorContains(t, err, "max_samples")
	_, _, err = (&evalContext{}).buildRunDataSource(t.Context(), nil, "", 0)
	require.Error(t, err)
	for _, value := range []string{"0", "1"} {
		command := &cobra.Command{}
		command.Flags().Int("max-samples", 0, "")
		require.NoError(t, command.Flags().Set("max-samples", value))
		_, err := runMaxSamples(command, 0, &group)
		require.ErrorContains(t, err, "--max-samples")
	}
}
