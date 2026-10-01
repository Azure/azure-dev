// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunRejectsEmptyDatasetOverrideAcrossSources(t *testing.T) {
	for _, mode := range []string{"dataset", "traces", "responses", "local", "id"} {
		for _, value := range []string{"", "  "} {
			t.Run(mode+"/"+value, func(t *testing.T) {
				dir := localSourceConfig(t, oneRow, 0)
				if mode != "local" {
					editLocalSourceConfig(t, dir, func(eval map[string]any) {
						if mode == "dataset" {
							delete(eval, "source")
							eval["dataset"] = "golden"
						} else {
							eval["source"] = map[string]any{"type": mode}
						}
					})
				}
				ec, requests := localSourceContext(t)
				name := "local-quality"
				if mode == "id" {
					name = "eval_local"
				}
				_, err := startLocalSource(t, ec, dir, name, map[string]string{"dataset": value})
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeInvalidParameter, local.Code)
				assert.Contains(t, local.Message, "--dataset")
				assert.Empty(t, recordedIdentityRequests(requests))
			})
		}
	}
}

func TestLocalMissingCatalogReferenceRequiresDirectContract(t *testing.T) {
	for _, operation := range []string{"create", "up", "run"} {
		for _, bad := range []bool{false, true} {
			t.Run(operation+"/"+fmt.Sprint(bad), func(t *testing.T) {
				rows := "{\"count\":9007199254740993}\n"
				if bad {
					rows += "{\"count\":\"wrong type\"}\n"
				}
				dir := localSourceConfig(t, rows, 1)
				cfg, err := project.OpenEvalConfig(dir)
				require.NoError(t, err)
				cfg.Evals[0].Evaluators[0].Evaluator = "custom.valid"
				cfg.Evals[0].Evaluators[0].DataMapping = map[string]string{"n": "{{item.count}}"}
				cfg.Evaluators = []project.EvaluatorDecl{{Name: "custom.valid"}}
				writeLocalContractConfig(t, dir, cfg)
				ec, requests := localSelectedContractContext(t, 0)
				ec.schemas = map[string]*eval_api.EvaluatorSummary{} // Successful, lagging catalog.
				switch operation {
				case "create":
					ec.state = map[string]string{}
					err = runLocalCreate(t, ec, dir, "local-quality")
				case "up":
					ec.state = map[string]string{}
					err = deployLocalContractConfig(t, ec, dir, cfg)
				case "run":
					_, err = startLocalSource(t, ec, dir, "local-quality", nil)
				}
				recorded := recordedIdentityRequests(requests)
				require.GreaterOrEqual(t, len(recorded), 2)
				assert.Equal(t, "/evaluators/custom.valid/versions", recorded[0].path)
				assert.Equal(t, "/evaluators/custom.valid/versions/7", recorded[1].path)
				if bad {
					require.ErrorContains(t, err, "local row 2")
					for _, req := range recorded {
						assert.Equal(t, http.MethodGet, req.method)
					}
				} else {
					require.NoError(t, err)
					assert.Equal(t, http.MethodPost, recorded[len(recorded)-1].method)
				}
				assert.Empty(t, ec.schemas, "direct reads must not replace the incomplete catalog snapshot")
			})
		}
	}
}

func TestLocalMissingCatalogReadFailureMakesNoSubmission(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			dir := localSourceConfig(t, "{\"count\":\"legacy would allow this\"}\n", 0)
			editLocalSourceConfig(t, dir, func(eval map[string]any) {
				eval["evaluators"] = []any{map[string]any{
					"evaluator": "custom.valid", "data_mapping": map[string]string{"n": "{{item.count}}"},
				}}
			})
			ec, requests := localSelectedContractContext(t, status)
			ec.schemas = map[string]*eval_api.EvaluatorSummary{}
			_, err := startLocalSource(t, ec, dir, "local-quality", nil)
			require.Error(t, err)
			for _, req := range recordedIdentityRequests(requests) {
				assert.Equal(t, http.MethodGet, req.method)
			}
		})
	}
}

func TestLocalStreamingRetainsOnlyCapAndValidatesEveryRow(t *testing.T) {
	const total = 12000
	dir := localSourceConfig(t, "", 1)
	path := filepath.Join(dir, "local rows.jsonl")
	file, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	require.NoError(t, err)
	padding := strings.Repeat("x", 1024)
	for range total {
		_, err := fmt.Fprintf(file, "{\"query\":%q}\n", padding)
		require.NoError(t, err)
	}
	require.NoError(t, file.Close())
	cfg, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	input, err := openLocalInput(t.Context(), &cfg.Evals[0], path)
	require.NoError(t, err)
	defer input.file.Close()
	visited := 0
	rows, err := input.collect(t.Context(), 1, func(row map[string]any, i int) error {
		visited++
		require.Len(t, row["query"], len(padding))
		return nil
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, total, visited)
	runtime.GC()
	runtime.ReadMemStats(&after)
	var growth uint64
	if after.HeapAlloc > before.HeapAlloc {
		growth = after.HeapAlloc - before.HeapAlloc
	}
	assert.Less(t, growth, uint64(8*1024*1024),
		"retained memory must stay well below the 12MiB source despite scanning every row")
	runtime.KeepAlive(rows)
	_, err = input.collect(t.Context(), 1, func(_ map[string]any, i int) error {
		if i == total-1 {
			return errors.New("invalid last row")
		}
		return nil
	})
	require.ErrorContains(t, err, "invalid last row")
}

func TestLocalStreamingRejectsFileChangedBetweenPasses(t *testing.T) {
	dir := localSourceConfig(t, oneRow, 1)
	path := filepath.Join(dir, "local rows.jsonl")
	cfg, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	input, err := openLocalInput(t.Context(), &cfg.Evals[0], path)
	require.NoError(t, err)
	defer input.file.Close()
	require.NoError(t, os.WriteFile(path, []byte("{\"query\":\"changed\"}\n"), 0o600))
	_, err = input.collect(t.Context(), 1)
	require.ErrorContains(t, err, "changed during validation")
}

func TestLocalImmutableRequestChangeRecreatesButCapDoesNot(t *testing.T) {
	dir := localSourceConfig(t, "{\"query\":\"first\"}\n", 1)
	cfg, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	cfg.Evals[0].Evaluators[0].DataMapping = nil
	state := &testEnvServer{state: map[string]string{}}
	created := map[string]*eval_api.CreateOpenAIEvalRequest{}
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/openai/v1/evals" {
			var request eval_api.CreateOpenAIEvalRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			count++
			id := fmt.Sprintf("eval_%d", count)
			created[id] = &request
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]string{"id": id}))
			return
		}
		if r.Method == http.MethodGet {
			id := filepath.Base(r.URL.Path)
			req := created[id]
			if req == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id": id, "name": req.Name, "metadata": req.Metadata,
				"data_source_config": req.DataSourceConfig, "testing_criteria": req.TestingCriteria,
			}))
			return
		}
		t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	run := func(cap int) (string, bool) {
		ec := evalContextFor(server)
		ec.azdClient = newTestAzdClient(t, state)
		ec.envName, ec.rootKnown = "test", true
		ec.schemas = map[string]*eval_api.EvaluatorSummary{"builtin.relevance": {
			Name: "builtin.relevance", Definition: &eval_api.EvaluatorContract{DataSchema: &eval_api.JSONSchema{
				Type: "object", Required: []string{"query"},
				Properties: map[string]any{
					"query": map[string]any{"type": "string"}, "extra": map[string]any{"type": "string"},
				},
			}},
		}}
		group := cfg.Evals[0]
		group.MaxSamples = cap
		id, changed, err := (&evalReconciler{ec: ec}).EnsureEval(t.Context(), group, group.LocalSourcePath(dir))
		require.NoError(t, err)
		return id, changed
	}
	first, changed := run(1)
	assert.True(t, changed)
	second, changed := run(0)
	assert.False(t, changed)
	assert.Equal(t, first, second)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "local rows.jsonl"),
		[]byte("{\"query\":\"second\",\"extra\":\"now available\"}\n"), 0o600))
	third, changed := run(1)
	assert.True(t, changed)
	assert.NotEqual(t, first, third)
	assert.Contains(t, created[third].TestingCriteria[0].DataMapping, "extra")
	fourth, changed := run(5)
	assert.False(t, changed)
	assert.Equal(t, third, fourth)
	assert.Equal(t, 2, count)
}
