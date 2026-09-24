// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/santhosh-tekuri/jsonschema/v6"
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

func TestResponseSchemaCallersRejectExplicitIDBeforePublication(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			service.eval = true
			service.definition = `{"name":"custom","version":"1","definition":{"data_schema":{"properties":{}}}}`
			cfg.Evaluators = []project.EvaluatorDecl{{
				Name: "custom", Definition: map[string]any{"type": "rubric", "dimensions": []any{}},
			}}
			cfg.Evals = []project.Eval{responseGroup()}
			cfg.Evals[0].ID = "eval_valid"
			cfg.Evals[0].Evaluators = evalcore.EvaluatorList{{Evaluator: "custom"}}
			var err error
			if caller == "create" {
				command := jsonCmd(t, "json")
				command.SetContext(t.Context())
				var out bytes.Buffer
				command.SetOut(&out)
				err = (&evalCreateAction{cmd: command}).create(
					ec, cfg, &cfg.Evals[0], filepath.Join(dir, project.EvalConfigBase))
				assert.Empty(t, out.String())
			} else {
				_, err = deployValidationFixture(t, t.Context(), ec, cfg, dir)
			}
			require.ErrorContains(t, err, "stored-responses schema")
			for _, request := range service.requests {
				assert.True(t, strings.HasPrefix(request, "GET "), "unexpected mutation: %s", request)
			}
			assert.Empty(t, env.config)
			assert.Empty(t, env.values)
			assert.False(t, service.dataset)
			assert.Zero(t, service.createCount)
		})
	}
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

func TestNonResponseSchemaMismatchIsNotReused(t *testing.T) {
	for _, lookup := range []string{"explicit ID", "cached", "renamed"} {
		t.Run(lookup, func(t *testing.T) {
			group := project.Eval{Name: "quality", Dataset: "d"}
			digest, err := project.FingerprintGroup(group)
			require.NoError(t, err)
			creates := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/eval_old"):
					_, _ = io.WriteString(w, `{"id":"eval_old","name":"old-name","data_source_config":{
						"type":"azure_ai_source","scenario":"traces_preview"}}`)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/evals"):
					creates++
					var request eval_api.CreateOpenAIEvalRequest
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					if assert.NotNil(t, request.DataSourceConfig) {
						assert.Equal(t, "custom", request.DataSourceConfig.Type)
					}
					_, _ = io.WriteString(w, `{"id":"eval_new"}`)
				default:
					t.Errorf("must not mutate an incompatible eval: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(srv.Close)
			ec := evalContextFor(srv)
			ec.schemas = map[string]*eval_api.EvaluatorSummary{}
			ec.state = map[string]string{}
			switch lookup {
			case "explicit ID":
				group.ID = "eval_old"
			case "cached":
				ec.state[idKey("eval", group.Name)] = "eval_old"
			case "renamed":
				ec.state[digestIDKey(digest)] = "eval_old"
			}
			id, changed, err := (&evalReconciler{ec: ec}).EnsureEval(t.Context(), group, "")
			if lookup == "explicit ID" {
				require.ErrorContains(t, err, "custom schema")
				assert.Empty(t, id)
				assert.False(t, changed)
				assert.Zero(t, creates)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "eval_new", id)
				assert.True(t, changed)
				assert.Equal(t, 1, creates)
			}
		})
	}
}

func TestNonResponseSchemaCompatibilityPreservesUnknownHistory(t *testing.T) {
	group := &project.Eval{Name: "quality", Dataset: "d"}
	for _, tc := range []struct {
		name   string
		config map[string]any
		match  bool
	}{
		{"legacy projection", nil, true},
		{"custom", map[string]any{"type": "custom"}, true},
		{"logs", map[string]any{"type": "logs"}, false},
		{"malformed type", map[string]any{"type": false}, false},
		{"empty type", map[string]any{"type": ""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.match, responseSchemaMatches(group, &eval_api.OpenAIEval{DataSourceConfig: tc.config}))
		})
	}
}

func TestResponseRunCallerPreservesFixedIDsAndRejectsLegacySources(t *testing.T) {
	for _, mode := range []string{
		"valid", "custom eval", "bare rows", "missing params", "read failure", "explicit cap zero", "explicit cap one",
		"alternate mapped key", "missing mapped key", "empty item", "empty ID", "whitespace ID", "non-string ID",
		"invalid mapping", "invalid later item",
	} {
		t.Run(mode, func(t *testing.T) {
			source := eval_api.NewResponsesDataSource([]string{"resp_fixed"}, 1)
			if mode == "bare rows" {
				source.ItemGenerationParams.Source.Content = []map[string]any{{"response_id": "resp_fixed"}}
			}
			if mode == "missing params" {
				source.ItemGenerationParams = nil
			}
			switch mode {
			case "alternate mapped key":
				source.ItemGenerationParams.DataMapping["response_id"] = "{{item.resp_id}}"
				source.ItemGenerationParams.Source.Content = []map[string]any{
					{"item": map[string]any{"resp_id": "resp_fixed"}},
				}
			case "missing mapped key":
				source.ItemGenerationParams.DataMapping["response_id"] = "{{item.missing}}"
			case "empty item":
				source.ItemGenerationParams.Source.Content = []map[string]any{{"item": map[string]any{}}}
			case "empty ID", "whitespace ID", "non-string ID":
				var value any = ""
				if mode == "whitespace ID" {
					value = " \t\u2003"
				} else if mode == "non-string ID" {
					value = 42
				}
				source.ItemGenerationParams.Source.Content = []map[string]any{{"item": map[string]any{"response_id": value}}}
			case "invalid mapping":
				source.ItemGenerationParams.DataMapping["response_id"] = "{{sample.response_id}}"
			case "invalid later item":
				source.ItemGenerationParams.Source.Content = append(source.ItemGenerationParams.Source.Content,
					map[string]any{"item": map[string]any{"response_id": ""}})
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
			command.SetContext(t.Context())
			command.SetOut(&out)
			command.Flags().String("output", "json", "")
			flags := &runStartFlags{
				groupName: "eval_fixed", evalPath: t.TempDir(),
			}
			command.Flags().IntVar(&flags.maxSamples, "max-samples", 0, "")
			if mode == "explicit cap zero" {
				require.NoError(t, command.Flags().Set("max-samples", "0"))
			}
			if mode == "explicit cap one" {
				require.NoError(t, command.Flags().Set("max-samples", "1"))
			}
			action := &runStartAction{
				cmd: command, flags: flags,
				newContext: func(context.Context, string) (*evalContext, error) {
					return evalContextFor(srv), nil
				},
			}
			err = action.Run()
			if mode == "valid" || mode == "alternate mapped key" {
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

func TestResponseSourceSchemaAndRuntimeAgree(t *testing.T) {
	const resourceURI = "https://example.test/responses.schema.json"
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource(resourceURI, evalSchemaDocument(t)))
	schema, err := compiler.Compile(resourceURI)
	require.NoError(t, err)
	for _, tc := range []struct {
		name    string
		ids     []string
		omitIDs bool
		cap     *int
		traces  bool
		ref     bool
		wantErr bool
	}{
		{name: "valid", ids: []string{"resp_fixed"}},
		{name: "missing IDs", omitIDs: true, wantErr: true},
		{name: "null IDs", wantErr: true},
		{name: "empty IDs", ids: []string{}, wantErr: true},
		{name: "empty ID", ids: []string{""}, wantErr: true},
		{name: "blank ID", ids: []string{" \t\r\n"}, wantErr: true},
		{name: "Unicode whitespace ID", ids: []string{"\u0085\u00a0\u2003"}, wantErr: true},
		{name: "blank later ID", ids: []string{"resp_fixed", " "}, wantErr: true},
		{name: "omitted cap", ids: []string{"resp_fixed"}},
		{name: "zero cap", ids: []string{"resp_fixed"}, cap: new(0)},
		{name: "positive cap", ids: []string{"resp_fixed"}, cap: new(1), wantErr: true},
		{name: "negative cap", ids: []string{"resp_fixed"}, cap: new(-1), wantErr: true},
		{name: "trace positive cap", traces: true, cap: new(1), wantErr: true},
		{name: "referenced response omitted cap", ids: []string{"resp_fixed"}, ref: true},
		{name: "referenced response zero cap", ids: []string{"resp_fixed"}, ref: true, cap: new(0)},
		{name: "referenced response positive cap", ids: []string{"resp_fixed"}, ref: true, cap: new(1), wantErr: true},
		{name: "referenced trace omitted cap", traces: true, ref: true},
		{name: "referenced trace zero cap", traces: true, ref: true, cap: new(0)},
		{name: "referenced trace positive cap", traces: true, ref: true, cap: new(1), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := map[string]any{"type": "responses"}
			if !tc.omitIDs {
				source["response_ids"] = tc.ids
			}
			if tc.traces {
				source = map[string]any{"type": "traces", "agent_name": "agent"}
			}
			dir := t.TempDir()
			if tc.ref {
				raw, err := json.Marshal(source)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "source.json"), raw, 0o600))
				source = map[string]any{"$ref": "./source.json"}
			}
			eval := map[string]any{
				"name": "source-eval", "source": source,
				"evaluators": []any{map[string]any{"evaluator": "builtin.coherence"}},
			}
			if tc.cap != nil {
				eval["max_samples"] = *tc.cap
			}
			body, err := json.Marshal(map[string]any{"evals": []any{eval}})
			require.NoError(t, err)
			var instance any
			require.NoError(t, json.Unmarshal(body, &instance))
			schemaErr := schema.Validate(instance)
			path := filepath.Join(dir, project.EvalConfigBase)
			require.NoError(t, os.WriteFile(path, body, 0o600))
			cfg, err := project.LoadEvalConfig(path)
			require.NoError(t, err)
			runtimeErr := cfg.Validate()
			if tc.wantErr {
				assert.Error(t, schemaErr)
				assert.Error(t, runtimeErr)
				if tc.cap != nil && *tc.cap > 0 {
					local, ok := errors.AsType[*azdext.LocalError](runtimeErr)
					require.True(t, ok)
					assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
					assert.Contains(t, azdext.WrapError(runtimeErr).GetMessage(), "source-eval")
				}
			} else {
				assert.NoError(t, schemaErr)
				assert.NoError(t, runtimeErr)
			}
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
}
