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
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalRequestBaselineDeleteKeepsOtherOwners(t *testing.T) {
	ec, env, _ := evalDeleteFixture(t, map[string]string{
		idKey("eval", "deleted"):    deletedEvalID,
		localRequestKey("deleted"):  "removed-contract",
		idKey("eval", "survivor"):   "eval_survivor",
		localRequestKey("survivor"): "preserved-contract",
	})
	require.NoError(t, ec.deleteEvalState(t.Context(), deletedEvalID))
	assert.Empty(t, env.stored(t, localRequestKey("deleted")))
	assert.Equal(t, "preserved-contract", env.stored(t, localRequestKey("survivor")))
}

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
				assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
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
					"evaluator": "custom.valid", "dataMapping": map[string]string{"n": "{{item.count}}"},
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
	// HeapAlloc covers the entire process, so run this workload without other package tests.
	const helper = "AZD_TEST_LOCAL_STREAMING_MEMORY"
	if os.Getenv(helper) != "1" {
		binary, err := os.Executable()
		require.NoError(t, err)
		child := exec.CommandContext(t.Context(), binary,
			"-test.run=^TestLocalStreamingRetainsOnlyCapAndValidatesEveryRow$",
			"-test.count=1", "-test.timeout=60s", "-test.v")
		child.Env = append(os.Environ(), helper+"=1")
		output, err := child.CombinedOutput()
		require.NoError(t, err, "%s", output)
		require.Contains(t, string(output), "--- PASS: "+t.Name()+" (")
		return
	}

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

// Two azure.ai.eval configurations can declare an eval with the same name but
// different local item schemas -- an ordinary layout per EvalScope's own
// rationale. Only the id and digest aliases are config-scoped; the immutable
// local-request baseline must be too, or the second configuration's deploy
// overwrites the first's baseline and forces an unrelated recreation on its
// next deploy, forking that eval's run history.
func TestLocalRequestBaselineIsScopedAcrossConfigurations(t *testing.T) {
	dirA := localSourceConfig(t, "{\"query\":\"first\"}\n", 1)
	dirB := localSourceConfig(t, "{\"query\":\"second\",\"extra\":\"now available\"}\n", 1)
	cfgA, err := project.OpenEvalConfig(dirA)
	require.NoError(t, err)
	cfgA.Evals[0].Evaluators[0].DataMapping = nil
	cfgB, err := project.OpenEvalConfig(dirB)
	require.NoError(t, err)
	cfgB.Evals[0].Evaluators[0].DataMapping = nil
	groupA, groupB := cfgA.Evals[0], cfgB.Evals[0]

	// The declared structures must be indistinguishable to the shared
	// definition baseline: only their local file content differs, which is
	// precisely the gap the local-request fingerprint exists to cover.
	digestA, err := project.FingerprintGroup(groupA)
	require.NoError(t, err)
	digestB, err := project.FingerprintGroup(groupB)
	require.NoError(t, err)
	require.Equal(t, digestA, digestB, "fixture must isolate the local-request fingerprint, not the declared digest")

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

	ensure := func(scope string, group project.Eval, dir string) (string, bool) {
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
		r := &evalReconciler{ec: ec, scope: scope}
		id, changed, err := r.EnsureEval(t.Context(), group, group.LocalSourcePath(dir))
		require.NoError(t, err)
		return id, changed
	}

	firstA, changed := ensure(scopeA, groupA, dirA)
	assert.True(t, changed)
	firstB, changed := ensure(scopeB, groupB, dirB)
	assert.True(t, changed)
	require.NotEqual(t, firstA, firstB)

	secondA, changed := ensure(scopeA, groupA, dirA)
	assert.False(t, changed, "a different configuration's local contract must not fork this eval's history")
	assert.Equal(t, firstA, secondA)

	secondB, changed := ensure(scopeB, groupB, dirB)
	assert.False(t, changed)
	assert.Equal(t, firstB, secondB)

	assert.Equal(t, 2, count, "only the two distinct local contracts create an eval")
}

func localIdentityFixture(t *testing.T) (*evalContext, *testEnvServer, *catalogPinService, *project.EvalConfig, string) {
	t.Helper()
	ec, env, service, cfg, dir := newCatalogPinFixture(t)
	cfg.Evaluators[0].Version = ""
	cfg.Evals[0].Source = &project.SourceDecl{Type: project.SourceTypeLocal, File: "rows.jsonl"}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte("{\"query\":\"first\"}\n"), 0o600))
	service.versions = map[string]json.RawMessage{}
	for _, version := range []string{"1", "2", "3"} {
		service.versions[version] = json.RawMessage(fmt.Sprintf(`{"name":"custom","version":%q,
			"definition":{"data_schema":{"type":"object","required":["query"],
			"properties":{"query":{"type":"string"},"extra":{"type":"string"}}}}}`, version))
	}
	return ec, env, service, cfg, dir
}

func reconcileLocalIdentity(
	t *testing.T, caller string, ec *evalContext, env *testEnvServer, cfg *project.EvalConfig, dir string,
) string {
	t.Helper()
	fresh := *ec
	fresh.azdClient = newTestAzdClient(t, env)
	fresh.state, fresh.schemas = nil, nil
	if caller == "create" {
		writeLocalContractConfig(t, dir, cfg)
		require.NoError(t, runLocalCreate(t, &fresh, dir, cfg.Evals[0].Name))
	} else {
		require.NoError(t, deployLocalContractConfig(t, &fresh, dir, cfg))
	}
	if cfg.Evals[0].ID != "" {
		return cfg.Evals[0].ID
	}
	return env.stored(t, idKey("eval", cfg.Evals[0].Name))
}

func TestLocalServiceLatestEchoPreservesHistory(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := localIdentityFixture(t)
			first := reconcileLocalIdentity(t, caller, ec, env, cfg, dir)
			stored := service.evals[first]
			stored.TestingCriteria = slices.Clone(stored.TestingCriteria)
			stored.TestingCriteria[0].EvaluatorVersion = "latest"
			service.latest = "3"
			assert.Equal(t, first, reconcileLocalIdentity(t, caller, ec, env, cfg, dir))
			cfg.Evals[0].MaxSamples = 1
			assert.Equal(t, first, reconcileLocalIdentity(t, caller, ec, env, cfg, dir))
			cfg.Evals[0].Name = "renamed"
			assert.Equal(t, first, reconcileLocalIdentity(t, caller, ec, env, cfg, dir))
			assert.Equal(t, "renamed", stored.Name)
			assert.Equal(t, first, reconcileLocalIdentity(t, caller, ec, env, cfg, dir))
			cfg.Evals[0].ID = first
			assert.Equal(t, first, reconcileLocalIdentity(t, caller, ec, env, cfg, dir))
			require.Len(t, service.created, 1)
			assert.Empty(t, service.created[0].TestingCriteria[0].EvaluatorVersion)
			assert.Equal(t, "latest", stored.TestingCriteria[0].EvaluatorVersion,
				"comparison must not mutate the service's immutable criteria")
			assert.Zero(t, service.publishes)
		})
	}
}

func TestLocalServiceEchoDoesNotHideImmutableDifferences(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, change := range []string{
			"authored pin", "pinned latest", "pinned version", "concrete unpinned echo", "schema", "mapping",
		} {
			t.Run(caller+"/"+change, func(t *testing.T) {
				ec, env, service, cfg, dir := localIdentityFixture(t)
				if strings.HasPrefix(change, "pinned") || change == "authored pin" {
					cfg.Evals[0].Evaluators[0].Version = "1"
				}
				first := reconcileLocalIdentity(t, caller, ec, env, cfg, dir)
				stored := service.evals[first]
				stored.TestingCriteria = slices.Clone(stored.TestingCriteria)
				switch change {
				case "authored pin":
					cfg.Evals[0].Evaluators[0].Version = "2"
				case "pinned latest":
					stored.TestingCriteria[0].EvaluatorVersion = "latest"
				case "pinned version", "concrete unpinned echo":
					stored.TestingCriteria[0].EvaluatorVersion = "2"
				case "schema":
					stored.DataSourceConfig["item_schema"] = map[string]any{"type": "object"}
				case "mapping":
					stored.TestingCriteria[0].DataMapping = map[string]string{"query": "{{item.other}}"}
				}
				second := reconcileLocalIdentity(t, caller, ec, env, cfg, dir)
				assert.NotEqual(t, first, second)
				assert.Same(t, stored, service.evals[first], "old eval and its run history must remain intact")
				assert.Equal(t, second, reconcileLocalIdentity(t, caller, ec, env, cfg, dir))
				require.Len(t, service.created, 2)
				assert.Equal(t, cfg.Evals[0].Evaluators[0].Version, service.created[1].TestingCriteria[0].EvaluatorVersion)
				assert.Zero(t, service.publishes)
			})
		}
	}
}

func TestLocalRenameWithRecycledNamePreservesHistory(t *testing.T) {
	for _, recycledFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(recycledFirst), func(t *testing.T) {
			ec, env, service, cfg, dir := localIdentityFixture(t)
			first := reconcileLocalIdentity(t, "up", ec, env, cfg, dir)
			stored := service.evals[first]
			originalRequest, err := json.Marshal(service.created[0])
			require.NoError(t, err)
			renamed := cfg.Evals[0]
			renamed.Name = "renamed"
			recycled := cfg.Evals[0]
			recycled.Source = &project.SourceDecl{Type: project.SourceTypeLocal, File: "different.jsonl"}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "different.jsonl"),
				[]byte("{\"query\":\"second\",\"extra\":\"new binding\"}\n"), 0o600))
			cfg.Evals = []project.Eval{renamed, recycled}
			if recycledFirst {
				slices.Reverse(cfg.Evals)
			}
			reconcileLocalIdentity(t, "up", ec, env, cfg, dir)
			assert.Equal(t, first, env.stored(t, idKey("eval", "renamed")))
			replacement := env.stored(t, idKey("eval", "quality"))
			assert.NotEqual(t, first, replacement)
			assert.Same(t, stored, service.evals[first])
			assert.Equal(t, "renamed", stored.Name)
			assert.NotContains(t, stored.TestingCriteria[0].DataMapping, "extra")
			assert.Contains(t, service.evals[replacement].TestingCriteria[0].DataMapping, "extra")
			after, err := json.Marshal(service.created[0])
			require.NoError(t, err)
			assert.JSONEq(t, string(originalRequest), string(after), "rename only changes mutable service fields")
			require.Len(t, service.created, 2, "only the recycled name creates an eval")
			reconcileLocalIdentity(t, "up", ec, env, cfg, dir)
			assert.Equal(t, first, env.stored(t, idKey("eval", "renamed")))
			assert.Equal(t, replacement, env.stored(t, idKey("eval", "quality")))
			assert.Len(t, service.created, 2, "unchanged deployment is idempotent")
			assert.Zero(t, service.publishes)
		})
	}
}

func TestLocalTargetedCreateDoesNotReleaseUnselectedOwner(t *testing.T) {
	ec, env, service, cfg, dir := localIdentityFixture(t)
	first := reconcileLocalIdentity(t, "create", ec, env, cfg, dir)
	owner := cfg.Evals[0]
	newcomer := owner
	newcomer.Name = "newcomer"
	owner.Source = &project.SourceDecl{Type: project.SourceTypeLocal, File: "missing-unselected.jsonl"}
	cfg.Evals = []project.Eval{newcomer, owner}
	second := reconcileLocalIdentity(t, "create", ec, env, cfg, dir)
	assert.NotEqual(t, first, second)
	assert.Equal(t, "quality", service.evals[first].Name)
	assert.Equal(t, first, env.stored(t, idKey("eval", "quality")))
	assert.Equal(t, second, reconcileLocalIdentity(t, "create", ec, env, cfg, dir))
	assert.Len(t, service.created, 2)
}

func TestLocalReservationRequiresKnownAbandonedContract(t *testing.T) {
	for _, evidence := range []string{"changed", "unknown", "explicit id"} {
		t.Run(evidence, func(t *testing.T) {
			ec, env, _, cfg, dir := localIdentityFixture(t)
			first := reconcileLocalIdentity(t, "create", ec, env, cfg, dir)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"),
				[]byte("{\"query\":\"changed\",\"extra\":\"new column\"}\n"), 0o600))
			if evidence == "unknown" {
				var state map[string]string
				require.NoError(t, json.Unmarshal(env.config[privateStatePath], &state))
				delete(state, localRequestKey("quality"))
				var err error
				env.config[privateStatePath], err = json.Marshal(state)
				require.NoError(t, err)
			} else if evidence == "explicit id" {
				cfg.Evals[0].ID = first
			}
			ec.state = nil
			r := &evalReconciler{ec: ec}
			require.NoError(t, r.Validate(t.Context(), cfg, dir))
			r.ReserveDeclared(t.Context(), cfg.Evals)
			if evidence == "changed" {
				assert.Empty(t, r.claimedBy[first])
			} else {
				assert.Equal(t, "quality", r.claimedBy[first])
			}
		})
	}
}

func TestLocalRequestEchoNormalizationKeepsExplicitLatest(t *testing.T) {
	req := &eval_api.CreateOpenAIEvalRequest{
		DataSourceConfig: &eval_api.DataSourceConfig{Type: "custom"},
		TestingCriteria: []eval_api.TestingCriterion{{
			Type: "azure_ai_evaluator", Name: "criterion", EvaluatorName: "custom", EvaluatorVersion: "latest",
		}},
	}
	remote := &eval_api.OpenAIEval{
		DataSourceConfig: map[string]any{"type": "custom"}, TestingCriteria: slices.Clone(req.TestingCriteria),
	}
	match, err := localRequestMatchesRemote(req, remote)
	require.NoError(t, err)
	assert.True(t, match)
	remote.TestingCriteria[0].EvaluatorVersion = ""
	match, err = localRequestMatchesRemote(req, remote)
	require.NoError(t, err)
	assert.False(t, match, "an explicitly requested version must not be normalized away")
	assert.Equal(t, "latest", req.TestingCriteria[0].EvaluatorVersion)
}
