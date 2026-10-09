// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func localSourceConfig(t *testing.T, rows string, cap int) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "local rows.jsonl"), []byte(rows), 0o600))
	body, err := json.Marshal(map[string]any{"evals": []any{map[string]any{
		"name":       "local-quality",
		"source":     map[string]any{"type": "local", "file": "./local rows.jsonl"},
		"maxSamples": cap,
		"evaluators": []any{map[string]any{"evaluator": "builtin.relevance", "dataMapping": map[string]string{
			"query": "{{item.query}}",
		}}},
	}}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.eval.yaml"), body, 0o600))
	return dir
}

func TestExplicitLocalRowsRejectNonRegularFiles(t *testing.T) {
	dir := localSourceConfig(t, oneRow, 0)
	cfg, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	for _, path := range []string{dir, os.DevNull} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.False(t, info.Mode().IsRegular())
			input, err := openLocalInput(t.Context(), &cfg.Evals[0], path)
			require.ErrorContains(t, err, "source.file must be a regular JSONL file")
			assert.Nil(t, input)
		})
	}
}

func localSourceContext(t *testing.T, alterDefinition ...func(map[string]any)) (*evalContext, <-chan identityRequest) {
	t.Helper()
	requests := make(chan identityRequest, 20)
	var last eval_api.CreateOpenAIEvalRunRequest
	var ec *evalContext
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		requests <- identityRequest{r.Method, r.URL.Path, body}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/evaluators":
			w.WriteHeader(http.StatusForbidden)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/evaluators/"):
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/evaluators/"), "/")
			require.GreaterOrEqual(t, len(parts), 2)
			contract := ec.schemas[parts[0]]
			if len(parts) == 3 {
				if selected := ec.schemas[evaluatorSchemaKey(parts[0], parts[2])]; selected != nil {
					contract = selected
				}
			}
			if contract == nil {
				t.Errorf("unexpected evaluator read: %s", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if len(parts) == 2 && parts[1] == "versions" {
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"value": []any{
					map[string]string{"name": parts[0], "version": "1"},
				}}))
			} else {
				assert.NoError(t, json.NewEncoder(w).Encode(contract))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/openai/v1/evals/eval_local":
			var definition map[string]any
			err := json.Unmarshal([]byte(`{"id":"eval_local","data_source_config":{"type":"custom","item_schema":`+
				`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}},`+
				`"testing_criteria":[{"name":"relevance","data_mapping":{"query":"{{item.query}}"}}]}`), &definition)
			assert.NoError(t, err)
			for _, alter := range alterDefinition {
				alter(definition)
			}
			assert.NoError(t, json.NewEncoder(w).Encode(definition))
		case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/evals/eval_local/runs":
			assert.NoError(t, json.Unmarshal(body, &last))
			_, _ = io.WriteString(w, `{"id":"evalrun_local","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/openai/v1/evals/eval_local/runs":
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": []eval_api.OpenAIEvalRun{{
				ID: "evalrun_local", DataSource: last.DataSource, Metadata: last.Metadata,
				EvaluationLevel: last.EvaluationLevel,
			}}}))
		case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/evals":
			_, _ = io.WriteString(w, `{"id":"eval_local"}`)
		default:
			t.Errorf("unexpected service call: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	ec = evalContextFor(server)
	ec.schemas = map[string]*eval_api.EvaluatorSummary{
		"builtin.relevance": {
			Name: "builtin.relevance", Definition: &eval_api.EvaluatorContract{DataSchema: &eval_api.JSONSchema{
				Type: "object", Required: []string{"query"},
				Properties: map[string]any{"query": map[string]any{"type": "string"}},
			}},
		},
	}
	ec.state = map[string]string{idKey("eval", "local-quality"): "eval_local"}
	return ec, requests
}

func startLocalSource(
	t *testing.T, ec *evalContext, dir, eval string, flags map[string]string,
) (string, error) {
	t.Helper()
	cmd := buildRunCommand("start", "")
	cmd.Flags().String("output", "json", "")
	for name, value := range flags {
		require.NoError(t, cmd.Flags().Set(name, value))
	}
	cap, err := cmd.Flags().GetInt("max-samples")
	require.NoError(t, err)
	dataset, err := cmd.Flags().GetString("dataset")
	require.NoError(t, err)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	action := &runStartAction{cmd: cmd, flags: &runStartFlags{
		groupName: eval, evalPath: dir, maxSamples: cap, datasetName: dataset, wait: false,
	}}
	err = action.start(t.Context(), ec, gate{})
	return out.String(), err
}

func editLocalSourceConfig(t *testing.T, dir string, edit func(map[string]any)) {
	t.Helper()
	path := filepath.Join(dir, "azure.eval.yaml")
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var config map[string]any
	require.NoError(t, json.Unmarshal(body, &config))
	evals, ok := config["evals"].([]any)
	require.True(t, ok)
	eval, ok := evals[0].(map[string]any)
	require.True(t, ok)
	edit(eval)
	body, err = json.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o600))
}

func TestExplicitLocalRunUsesInlineRowsWithoutRegistryIdentity(t *testing.T) {
	dir := localSourceConfig(t, "{\"query\":\"first\"}\n{\"query\":\"second\"}\n", 1)
	ec, requests := localSourceContext(t)
	cmd := buildRunCommand("start", "")
	cmd.Flags().String("output", "json", "")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	action := &runStartAction{cmd: cmd, flags: &runStartFlags{
		groupName: "local-quality", evalPath: dir, wait: false,
	}}
	require.NoError(t, action.start(t.Context(), ec, gate{}))
	recorded := recordedIdentityRequests(requests)
	require.Len(t, recorded, 3)
	assert.Equal(t, http.MethodGet, recorded[0].method)
	assert.Equal(t, http.MethodGet, recorded[1].method)
	assert.Equal(t, "/openai/v1/evals/eval_local", recorded[1].path)
	assert.Equal(t, http.MethodPost, recorded[2].method)
	var submitted eval_api.CreateOpenAIEvalRunRequest
	require.NoError(t, json.Unmarshal(recorded[2].body, &submitted))
	assert.Equal(t, map[string]string{metaEvalName: "local-quality"}, submitted.Metadata)
	require.NotNil(t, submitted.DataSource)
	assert.Equal(t, eval_api.EvalRunDataSourceTypeJSONL, submitted.DataSource.Type)
	assert.Equal(t, &eval_api.EvalRunDataContent{
		Type:    eval_api.EvalRunDataContentTypeFileContent,
		Content: []map[string]any{{"query": "first"}},
	}, submitted.DataSource.Source)
	var handoff map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &handoff))
	assert.Equal(t, map[string]any{
		"eval_id": "eval_local", "run_id": "evalrun_local", "eval_name": "local-quality", "status": "queued",
	}, handoff)
	assert.NotContains(t, string(recorded[2].body), "local rows.jsonl")
}

func TestExplicitLocalCapsAndTargets(t *testing.T) {
	for _, target := range []string{"static", "agent", "model"} {
		for _, flag := range []string{"", "0", "1"} {
			t.Run(target+"/"+flag, func(t *testing.T) {
				dir := localSourceConfig(t, "{\"query\":\"first\"}\n{\"query\":\"second\"}\n", 1)
				if target != "static" {
					editLocalSourceConfig(t, dir, func(eval map[string]any) {
						eval["target"] = map[string]any{"type": target, "name": "target"}
					})
				}
				ec, requests := localSourceContext(t)
				// Even stale or unreadable dataset state cannot select a registered
				// source for explicitly local bytes.
				ec.state[versionKey("dataset", "local rows")] = "7"
				ec.stateErr = fmt.Errorf("unrelated dataset state unavailable")
				flags := map[string]string{}
				if flag != "" {
					flags["max-samples"] = flag
				}
				_, err := startLocalSource(t, ec, dir, "local-quality", flags)
				require.NoError(t, err)
				recorded := recordedIdentityRequests(requests)
				require.Len(t, recorded, 3)
				assert.Equal(t, http.MethodGet, recorded[0].method)
				assert.Equal(t, http.MethodGet, recorded[1].method)
				assert.Equal(t, http.MethodPost, recorded[2].method)
				var submitted eval_api.CreateOpenAIEvalRunRequest
				require.NoError(t, json.Unmarshal(recorded[2].body, &submitted))
				require.NotNil(t, submitted.DataSource)
				require.NotNil(t, submitted.DataSource.Source)
				want := 1
				if flag == "0" {
					want = 2
				}
				assert.Len(t, submitted.DataSource.Source.Content, want)
				assert.Equal(t, map[string]string{metaEvalName: "local-quality"}, submitted.Metadata)
				if target == "static" {
					assert.Nil(t, submitted.DataSource.Target)
				} else {
					require.NotNil(t, submitted.DataSource.Target)
					assert.Equal(t, "azure_ai_"+target, submitted.DataSource.Target.Type)
				}
				for _, request := range recorded {
					assert.NotContains(t, request.path, "/datasets")
				}
			})
		}
	}
}

type localSourceEnv struct {
	testEnvServer
	writes int
}

func (s *localSourceEnv) SetConfig(
	ctx context.Context, req *azdext.SetConfigRequest,
) (*azdext.EmptyResponse, error) {
	s.writes++
	return s.testEnvServer.SetConfig(ctx, req)
}

func TestExplicitLocalInvalidInputsNeverSubmitOrWriteState(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows string
		edit func(map[string]any)
		flag map[string]string
	}{
		{name: "malformed after cap", rows: "{\"query\":\"first\"}\nnot json\n"},
		{name: "two objects on one line", rows: "{\"query\":\"first\"} {\"query\":\"second\"}\n"},
		{name: "missing mapped column after cap", rows: "{\"query\":\"first\"}\n{\"other\":\"second\"}\n"},
		{name: "wrong mapped type after cap", rows: "{\"query\":\"first\"}\n{\"query\":2}\n"},
		{name: "null row", rows: "null\n"},
		{name: "empty object", rows: "{}\n"},
		{name: "array row", rows: "[]\n"},
		{name: "empty file"},
		{name: "missing file", rows: oneRow, edit: func(eval map[string]any) {
			eval["source"] = map[string]any{"type": "local", "file": "missing.jsonl"}
		}},
		{name: "directory", rows: oneRow, edit: func(eval map[string]any) {
			eval["source"] = map[string]any{"type": "local", "file": "."}
		}},
		{name: "mixed dataset", rows: oneRow, edit: func(eval map[string]any) { eval["dataset"] = "golden" }},
		{name: "empty mixed dataset", rows: oneRow, edit: func(eval map[string]any) { eval["dataset"] = "" }},
		{name: "simulation", rows: oneRow, edit: func(eval map[string]any) {
			eval["simulation"] = map[string]any{"model": "connection/model"}
		}},
		{name: "dataset flag", rows: oneRow, flag: map[string]string{"dataset": "golden"}},
		{name: "empty dataset flag", rows: oneRow, flag: map[string]string{"dataset": ""}},
		{name: "negative cap", rows: oneRow, edit: func(eval map[string]any) { eval["maxSamples"] = -1 }},
		{name: "target missing query", rows: "{\"response\":\"answer\"}\n", edit: func(eval map[string]any) {
			eval["target"] = map[string]any{"type": "agent", "name": "agent"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := localSourceConfig(t, tc.rows, 1)
			if tc.edit != nil {
				editLocalSourceConfig(t, dir, tc.edit)
			}
			ec, requests := localSourceContext(t)
			initial := maps.Clone(ec.state)
			rawState, err := json.Marshal(initial)
			require.NoError(t, err)
			env := &localSourceEnv{testEnvServer: testEnvServer{
				config: map[string][]byte{privateStatePath: rawState},
			}}
			ec.azdClient = newTestAzdClient(t, env)
			ec.envName, ec.root, ec.rootKnown = "test", dir, true
			output, err := startLocalSource(t, ec, dir, "local-quality", tc.flag)
			require.Error(t, err)
			assert.Empty(t, output)
			assert.Zero(t, env.writes)
			assert.Equal(t, initial, ec.state)
			assert.Equal(t, rawState, env.config[privateStatePath])
			assert.Empty(t, recordedIdentityRequests(requests), "local validation precedes any service call")
			if _, explicit := tc.flag["dataset"]; explicit {
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
			}
		})
	}
}

func TestExplicitLocalStoredMappingsAreValidatedBeforeSubmission(t *testing.T) {
	for _, mode := range []string{"missing mapped field", "missing schema", "external schema"} {
		t.Run(mode, func(t *testing.T) {
			dir := localSourceConfig(t, "{\"query\":\"first\"}\n{\"query\":\"second\"}\n", 1)
			ec, requests := localSourceContext(t, func(definition map[string]any) {
				switch mode {
				case "missing mapped field":
					definition["testing_criteria"] = []any{map[string]any{
						"name": "quality", "data_mapping": map[string]string{"answer": "{{item.answer}}"},
					}}
				case "missing schema":
					delete(definition, "data_source_config")
				case "external schema":
					// #nosec G101 -- synthetic credential-bearing URL verifies non-disclosure; never requested.
					definition["data_source_config"] = map[string]any{"item_schema": map[string]any{
						"$ref": "https://user:secret@must-not-be-contacted.invalid/schema.json?sig=secret#secret",
					}}
				}
			})
			_, err := startLocalSource(t, ec, dir, "local-quality", nil)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret")
			recorded := recordedIdentityRequests(requests)
			require.Len(t, recorded, 1)
			assert.Equal(t, http.MethodGet, recorded[0].method)
		})
	}
}

func TestExplicitLocalUnreadableEvaluatorContractsFailClosed(t *testing.T) {
	dir := localSourceConfig(t, oneRow, 0)
	ec, requests := localSourceContext(t)
	ec.schemas = nil
	_, err := startLocalSource(t, ec, dir, "local-quality", nil)
	require.Error(t, err)
	assert.Nil(t, ec.evaluatorSchemas(t.Context()), "existing non-local callers retain their best-effort fallback")
	for _, request := range recordedIdentityRequests(requests) {
		assert.Equal(t, http.MethodGet, request.method, "unreadable evaluator contracts must not permit submission")
	}
}

func TestExplicitLocalIDRerunUsesSnapshotNotFile(t *testing.T) {
	dir := localSourceConfig(t, "{\"query\":\"before edit\"}\n", 0)
	ec, requests := localSourceContext(t)
	_, err := startLocalSource(t, ec, dir, "local-quality", nil)
	require.NoError(t, err)
	first := recordedIdentityRequests(requests)
	require.Len(t, first, 3)
	require.NoError(t, os.Remove(filepath.Join(dir, "local rows.jsonl")))
	out, err := startLocalSource(t, ec, dir, "eval_local", nil)
	require.NoError(t, err)
	recorded := recordedIdentityRequests(requests)
	require.Len(t, recorded, 3)
	assert.Equal(t, "/openai/v1/evals/eval_local/runs", recorded[0].path)
	assert.Equal(t, "/openai/v1/evals/eval_local", recorded[1].path)
	var before, after eval_api.CreateOpenAIEvalRunRequest
	require.NoError(t, json.Unmarshal(first[2].body, &before))
	require.NoError(t, json.Unmarshal(recorded[2].body, &after))
	assert.Equal(t, before.DataSource, after.DataSource)
	assert.NotContains(t, after.Metadata, metaDataset)
	assert.NotContains(t, after.Metadata, metaDatasetVersion)
	assert.NotContains(t, out, "dataset")
	for _, request := range recorded {
		assert.False(t, strings.Contains(request.path, "/datasets"))
	}
}

func TestExplicitLocalNumbersRetainPrecision(t *testing.T) {
	dir := localSourceConfig(t, "{\"query\":\"local\",\"id\":9007199254740993}\n", 0)
	ec, requests := localSourceContext(t)
	_, err := startLocalSource(t, ec, dir, "local-quality", nil)
	require.NoError(t, err)
	recorded := recordedIdentityRequests(requests)
	require.Len(t, recorded, 3)
	assert.Contains(t, string(recorded[2].body), `"id":9007199254740993`)
	require.NoError(t, os.Remove(filepath.Join(dir, "local rows.jsonl")))
	_, err = startLocalSource(t, ec, dir, "eval_local", nil)
	require.NoError(t, err)
	recorded = recordedIdentityRequests(requests)
	require.Len(t, recorded, 3)
	assert.Contains(t, string(recorded[2].body), `"id":9007199254740993`)
}

func TestExplicitLocalCreateUsesAllRowsAndNeverPublishesDataset(t *testing.T) {
	for _, rows := range []string{"{\"query\":\"first\"}\n{\"query\":\"second\"}\n", "{\"query\":\"first\"}\n{}\n"} {
		t.Run(rows, func(t *testing.T) {
			dir := localSourceConfig(t, rows, 1)
			cfg, err := project.OpenEvalConfig(dir)
			require.NoError(t, err)
			group := cfg.Evals[0]
			ec, requests := localSourceContext(t)
			ec.state = map[string]string{}
			r := &evalReconciler{ec: ec}
			err = r.PreflightLocalEval(t.Context(), group, group.LocalSourcePath(dir))
			if strings.Contains(rows, "{}") {
				require.Error(t, err)
				assert.Empty(t, recordedIdentityRequests(requests))
				return
			}

			require.NoError(t, err)
			id, created, err := r.EnsureEval(t.Context(), group, group.LocalSourcePath(dir))
			require.NoError(t, err)
			assert.True(t, created)
			assert.Equal(t, "eval_local", id)
			recorded := recordedIdentityRequests(requests)
			require.Len(t, recorded, 1)
			assert.Equal(t, "/openai/v1/evals", recorded[0].path)
			assert.Equal(t, http.MethodPost, recorded[0].method)
			assert.NotContains(t, string(recorded[0].body), "local rows.jsonl")
		})
	}
}

func TestExplicitLocalDeployPreflightPrecedesPublication(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			rows := "{\"query\":\"local\"}\n"
			if !valid {
				rows += "{}\n"
			}
			root := localSourceConfig(t, rows, 1)
			body, err := os.ReadFile(filepath.Join(root, "azure.eval.yaml"))
			require.NoError(t, err)
			var config map[string]any
			require.NoError(t, json.Unmarshal(body, &config))
			if !valid {
				config["datasets"] = []any{map[string]any{"name": "unrelated", "file": "never-publish.jsonl"}}
				require.NoError(t, os.WriteFile(filepath.Join(root, "never-publish.jsonl"), []byte(oneRow), 0o600))
			}
			client := projectServingClient(t, &azdext.ProjectConfig{Path: root})
			ec, requests := localSourceContext(t)
			ec.state = map[string]string{}
			provider := project.NewEvalServiceTargetProvider(client,
				func(context.Context, string) (project.Reconciler, error) {
					return &evalReconciler{ec: ec}, nil
				})
			service := &azdext.ServiceConfig{
				Name: "evals", Host: project.EvalHost, AdditionalProperties: mustStruct(t, config),
			}
			_, err = provider.Deploy(t.Context(), service, nil, nil, nil)
			recorded := recordedIdentityRequests(requests)
			if !valid {
				require.ErrorContains(t, err, "empty object")
				for _, request := range recorded {
					assert.Equal(t, http.MethodGet, request.method, "bad local rows must stop even unrelated publication")
					assert.True(t, strings.HasPrefix(request.path, "/evaluators/"))
				}
				assert.Empty(t, ec.state)
			} else {
				require.NoError(t, err)
				require.Len(t, recorded, 3)
				assert.Equal(t, http.MethodGet, recorded[0].method)
				assert.Equal(t, "/evaluators/builtin.relevance/versions", recorded[0].path)
				assert.Equal(t, http.MethodGet, recorded[1].method)
				assert.Equal(t, "/evaluators/builtin.relevance/versions/1", recorded[1].path)
				assert.Equal(t, http.MethodPost, recorded[2].method)
				assert.Equal(t, "/openai/v1/evals", recorded[2].path)
				assert.NotContains(t, string(recorded[2].body), "local rows.jsonl")
			}
		})
	}
}
