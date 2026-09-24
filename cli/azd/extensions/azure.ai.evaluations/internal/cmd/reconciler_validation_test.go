// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

type validationService struct {
	mu          sync.Mutex
	requests    []string
	status      int
	dataset     bool
	eval        bool
	failCreate  bool
	definition  string
	createCount int
}

func (s *validationService) serve(t *testing.T, base func() string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/evaluators/"):
			if s.status != http.StatusOK {
				w.WriteHeader(s.status)
				_, _ = w.Write([]byte(`{"error":{"code":"ReferenceUnavailable"}}`))
				return
			}
			if strings.HasSuffix(r.URL.Path, "/versions") {
				_, _ = w.Write([]byte(`{"value":[{"name":"builtin.valid","version":"1"}]}`))
			} else {
				_, _ = w.Write([]byte(s.definition))
			}
		case strings.HasSuffix(r.URL.Path, "/startPendingUpload"):
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"blobReference": map[string]any{
					"blobUri": base() + "/blob", "storageAccountArmId": "test",
					"credential": map[string]any{"sasUri": base() + "/blob?sig=test"},
				},
			}))
		case strings.HasPrefix(r.URL.Path, "/blob/"):
			w.WriteHeader(http.StatusCreated)
		case strings.Contains(r.URL.Path, "/datasets/"):
			if r.Method == http.MethodPut {
				s.dataset = true
			}
			if strings.HasSuffix(r.URL.Path, "/versions") {
				if s.dataset {
					_, _ = w.Write([]byte(`{"value":[{"name":"turn-tests","version":"1.0"}]}`))
				} else {
					_, _ = w.Write([]byte(`{"value":[]}`))
				}
			} else if s.dataset {
				_, _ = w.Write([]byte(`{"name":"turn-tests","version":"1.0"}`))
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/evals"):
			s.createCount++
			if s.failCreate {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			s.eval = true
			_, _ = w.Write([]byte(`{"id":"eval_valid","name":"confirm-unknown-evaluator"}`))
		case strings.Contains(r.URL.Path, "/evals/") && s.eval:
			_, _ = w.Write([]byte(`{"id":"eval_valid","name":"confirm-unknown-evaluator"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func validationFixture(t *testing.T) (*evalContext, *testEnvServer, *validationService, *project.EvalConfig, string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte("{\"query\":\"hi\"}\n"), 0o600))
	service := &validationService{
		status: http.StatusOK,
		definition: `{"name":"builtin.valid","version":"1","definition":{"data_schema":` +
			`{"properties":{"query":{"type":"string"}},"required":["query"]}}}`,
	}
	var server *httptest.Server
	server = httptest.NewServer(service.serve(t, func() string { return server.URL }))
	t.Cleanup(server.Close)
	env := &testEnvServer{state: map[string]string{"unrelated": "preserve"}}
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	ec := &evalContext{
		azdClient: newTestAzdClient(t, env), envName: "test", rootKnown: true, root: dir,
		evalClient:    eval_api.NewEvalClientFromPipeline(server.URL, pipeline),
		datasetClient: dataset_api.NewDatasetClientFromPipeline(server.URL, pipeline),
	}
	cfg := &project.EvalConfig{
		Datasets: []project.DatasetDecl{{Name: "turn-tests", File: "rows.jsonl"}},
		Evals: []project.Eval{{
			Name: "confirm-unknown-evaluator", Dataset: "turn-tests",
			Evaluators: evalcore.EvaluatorList{{Evaluator: "builtin.valid"}},
		}},
	}
	return ec, env, service, cfg, dir
}

func deployValidationFixture(
	t *testing.T, ctx context.Context, ec *evalContext, cfg *project.EvalConfig, dir string,
) (string, error) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var values map[string]any
	require.NoError(t, json.Unmarshal(raw, &values))
	props, err := structpb.NewStruct(values)
	require.NoError(t, err)
	client := clientServing(t, &azdext.ProjectConfig{Path: dir})
	provider := project.NewEvalServiceTargetProvider(client,
		func(context.Context, string) (project.Reconciler, error) { return &evalReconciler{ec: ec}, nil })
	var progress bytes.Buffer
	_, err = provider.Deploy(ctx, &azdext.ServiceConfig{Name: "evals", AdditionalProperties: props},
		nil, nil, func(message string) { progress.WriteString(message) })
	return progress.String(), err
}

func TestReconciliationValidatesBeforeAnyMutation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *project.EvalConfig, *validationService, string)
	}{
		{"missing builtin", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			cfg.Evals[0].Evaluators[0].Evaluator = "builtin.does_not_exist"
			s.status = http.StatusNotFound
		}},
		{"missing custom", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			cfg.Evaluators = []project.EvaluatorDecl{{Name: "custom"}}
			cfg.Evals[0].Evaluators[0].Evaluator = "custom"
			s.status = http.StatusNotFound
		}},
		{"missing pinned version", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			cfg.Evals[0].Evaluators[0].Version = "999"
			s.status = http.StatusNotFound
		}},
		{"forbidden lookup", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			s.status = http.StatusForbidden
		}},
		{"failed lookup", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			s.status = http.StatusServiceUnavailable
		}},
		{"malformed contract", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			s.definition = `null`
		}},
		{"missing dataset file", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			cfg.Datasets[0].File = "missing.jsonl"
		}},
		{"malformed later row", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte("{\"query\":\"hi\"}\ninvalid"), 0o600))
		}},
		{"missing rubric", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			cfg.Evaluators = []project.EvaluatorDecl{{Name: "custom", Source: "missing.json"}}
			cfg.Evals[0].Evaluators = append(cfg.Evals[0].Evaluators, evalcore.EvaluatorRef{Evaluator: "custom"})
		}},
		{"malformed rubric", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte("null"), 0o600))
			cfg.Evaluators = []project.EvaluatorDecl{{Name: "custom", Source: "bad.json"}}
			cfg.Evals[0].Evaluators = append(cfg.Evals[0].Evaluators, evalcore.EvaluatorRef{Evaluator: "custom"})
		}},
		{"missing required column", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			s.definition = `{"definition":{"data_schema":{"properties":{"context":{}},"required":["context"]}}}`
		}},
		{"column absent on later row", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			rows := "{\"query\":\"hi\"}\n{\"response\":\"answer without a query\"}\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(rows), 0o600))
		}},
		{"missing explicit column", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			cfg.Evals[0].Evaluators[0].DataMapping = map[string]string{"query": "{{item.missing}}"}
		}},
		{"missing judge", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			s.definition = `{"definition":{"init_parameters":{"properties":{"model":{}},"required":["model"]}}}`
		}},
		{"local rubric missing judge", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			cfg.Evaluators = []project.EvaluatorDecl{{Name: "custom", Definition: map[string]any{"dimensions": []any{}}}}
			cfg.Evals[0].Evaluators[0].Evaluator = "custom"
			s.definition = `{"definition":{"init_parameters":{"properties":{"model":{}},"required":["model"]}}}`
		}},
		{"missing eval ID", func(t *testing.T, cfg *project.EvalConfig, s *validationService, dir string) {
			cfg.Evals[0].ID = "eval_missing"
		}},
	}
	for _, caller := range []string{"create", "up"} {
		for _, tc := range cases {
			t.Run(caller+"/"+tc.name, func(t *testing.T) {
				ec, env, service, cfg, dir := validationFixture(t)
				tc.change(t, cfg, service, dir)
				var err error
				if caller == "create" {
					cmd := jsonCmd(t, "json")
					cmd.SetContext(t.Context())
					var out bytes.Buffer
					cmd.SetOut(&out)
					err = (&evalCreateAction{cmd: cmd}).create(ec, cfg, &cfg.Evals[0], filepath.Join(dir, "azure.yaml"))
					assert.Empty(t, out.String())
				} else {
					_, err = deployValidationFixture(t, t.Context(), ec, cfg, dir)
				}
				require.Error(t, err)
				for _, request := range service.requests {
					assert.True(t, strings.HasPrefix(request, "GET "), "unexpected mutation: %s", request)
				}
				assert.Empty(t, env.config, "preflight must not write private state")
				assert.Empty(t, env.values, "preflight must not write public state")
				assert.Equal(t, "preserve", env.stored(t, "unrelated"))
				_, statErr := os.Stat(filepath.Join(dir, ".azure"))
				assert.ErrorIs(t, statErr, os.ErrNotExist, "validation must not create a state lock")
			})
		}
	}
}

func TestCreateIgnoresUnrelatedInvalidEvalAndIsIdempotent(t *testing.T) {
	ec, env, service, cfg, dir := validationFixture(t)
	cfg.Evals = append(cfg.Evals, project.Eval{Name: "unrelated", MaxSamples: -1})
	cfg.Datasets = append(cfg.Datasets, project.DatasetDecl{Name: "unrelated", File: "missing.jsonl"})
	require.NoError(t, cfg.ValidateForLookup())
	for range 2 {
		cmd := jsonCmd(t, "json")
		cmd.SetContext(t.Context())
		var out bytes.Buffer
		cmd.SetOut(&out)
		err := (&evalCreateAction{cmd: cmd}).create(ec, cfg, &cfg.Evals[0], filepath.Join(dir, "azure.yaml"))
		require.NoError(t, err)
		var result map[string]string
		require.NoError(t, json.Unmarshal(out.Bytes(), &result))
		assert.Equal(t, "eval_valid", result["id"])
	}
	assert.Equal(t, 1, service.createCount)
	uploads := 0
	for _, request := range service.requests {
		if strings.Contains(request, "startPendingUpload") {
			uploads++
		}
	}
	assert.Equal(t, 1, uploads)
	assert.Equal(t, "1.0", env.stored(t, versionKey("dataset", "turn-tests")))
}

func TestUpValidatesAllEvalsBeforePublishing(t *testing.T) {
	ec, env, service, cfg, dir := validationFixture(t)
	cfg.Evals = append(cfg.Evals, project.Eval{Name: "invalid", MaxSamples: -1})
	_, err := deployValidationFixture(t, t.Context(), ec, cfg, dir)
	require.Error(t, err)
	assert.Empty(t, service.requests)
	assert.Empty(t, env.config)
}

func TestLocalRubricRetainsPublishedLevelRestrictionsBeforeMutation(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			definition := `{"type":"rubric","dimensions":[{"id":"clarity","weight":5}]}`
			require.NoError(t, os.WriteFile(filepath.Join(dir, "quality.json"), []byte(definition), 0o600))
			service.definition = `{"name":"quality","version":"1","supported_evaluation_levels":["turn"],` +
				`"definition":` + definition + `}`
			cfg.Evaluators = []project.EvaluatorDecl{{Name: "quality", Source: "quality.json"}}
			cfg.Evals[0].Evaluators = evalcore.EvaluatorList{{Evaluator: "quality"}}
			cfg.Evals[0].EvaluationLevel = project.EvaluationLevelConversation

			err := reconcileArtifactConfig(t, caller, ec, cfg, dir)
			require.ErrorContains(t, err, "conversation")
			for _, request := range service.requests {
				assert.True(t, strings.HasPrefix(request, "GET "), "unexpected mutation: %s", request)
			}
			assert.Empty(t, env.config)
			assert.Empty(t, env.values)
			assert.Equal(t, "preserve", env.stored(t, "unrelated"))
		})
	}
}

func TestUpPreservesPublishedDependenciesAfterServiceFailure(t *testing.T) {
	ec, env, service, cfg, dir := validationFixture(t)
	service.failCreate = true
	progress, err := deployValidationFixture(t, t.Context(), ec, cfg, dir)
	require.Error(t, err)
	assert.Contains(t, progress, "1.0")
	assert.NotContains(t, progress, "eval_valid")
	assert.Equal(t, "1.0", env.stored(t, versionKey("dataset", "turn-tests")))
	assert.NotEmpty(t, env.stored(t, project.FingerprintKey("dataset", "turn-tests")))
	assert.Empty(t, env.stored(t, idKey("eval", cfg.Evals[0].Name)))
	service.failCreate = false
	for range 2 {
		_, err = deployValidationFixture(t, t.Context(), ec, cfg, dir)
		require.NoError(t, err)
	}
	uploads := 0
	for _, request := range service.requests {
		assert.NotContains(t, request, "DELETE ")
		if strings.Contains(request, "startPendingUpload") {
			uploads++
		}
	}
	assert.Equal(t, 1, uploads, "recovery must reuse the successful shared version")
	assert.Equal(t, 2, service.createCount, "one failed create and one successful create")
}

func TestCreateReportsRetainedDependenciesOnServiceFailure(t *testing.T) {
	ec, env, service, cfg, dir := validationFixture(t)
	service.failCreate = true
	cmd := jsonCmd(t, "json")
	cmd.SetContext(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := (&evalCreateAction{cmd: cmd}).create(ec, cfg, &cfg.Evals[0], filepath.Join(dir, "azure.yaml"))
	require.Error(t, err)
	var result struct {
		Status    string               `json:"status"`
		Artifacts []reconciledArtifact `json:"artifacts"`
		Error     jsonErrorBody        `json:"error"`
		Recovery  string               `json:"recovery_command"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	assert.Equal(t, "failed", result.Status)
	require.Len(t, result.Artifacts, 1)
	assert.Equal(t, reconciledArtifact{"dataset", "turn-tests", "1.0", true}, result.Artifacts[0])
	assert.NotEmpty(t, result.Error.Message)
	assert.Contains(t, result.Recovery, "azd ai eval create confirm-unknown-evaluator --from-file")
	assert.Equal(t, "1.0", env.stored(t, versionKey("dataset", "turn-tests")))
	assert.Empty(t, env.stored(t, idKey("eval", cfg.Evals[0].Name)))
}

func TestReconciliationValidationHonorsCancellation(t *testing.T) {
	ec, env, service, cfg, dir := validationFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := (&evalReconciler{ec: ec}).Validate(ctx, cfg, dir)
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, service.requests)
	assert.Empty(t, env.config)
}

func TestLocalRubricOverrideCannotHideReusedContract(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, metadata := range []string{"catalog", "document"} {
			for _, baseline := range []string{"absent", "recorded"} {
				t.Run(caller+"/"+metadata+"/"+baseline, func(t *testing.T) {
					ec, env, service, cfg, dir := validationFixture(t)
					definition := `{"type":"rubric","dimensions":[{"id":"clarity","weight":5}]}`
					body := definition
					decl := project.EvaluatorDecl{Name: "quality", Source: "quality.json"}
					if metadata == "catalog" {
						decl.SupportedEvaluationLevels = []string{"conversation"}
					} else {
						body = `{"supported_evaluation_levels":["conversation"],"definition":` + definition + `}`
					}
					path := filepath.Join(dir, decl.Source)
					require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
					if baseline == "recorded" {
						_, digest, err := localEvaluator(decl, path)
						require.NoError(t, err)
						env.state[project.FingerprintKey("evaluator", decl.Name)] = digest
					}
					service.definition = `{"name":"quality","version":"1","supported_evaluation_levels":["turn"],` +
						`"definition":` + definition + `}`
					cfg.Evaluators = []project.EvaluatorDecl{decl}
					cfg.Evals[0].Evaluators = evalcore.EvaluatorList{{Evaluator: decl.Name}}
					cfg.Evals[0].EvaluationLevel = project.EvaluationLevelConversation
					err := reconcileArtifactConfig(t, caller, ec, cfg, dir)
					require.ErrorContains(t, err, "conversation")
					for _, request := range service.requests {
						assert.True(t, strings.HasPrefix(request, "GET "), "unexpected mutation: %s", request)
					}
					assert.Empty(t, env.config, "the reused contract must be rejected before private-state writes")
					assert.Empty(t, env.values)
				})
			}
		}
	}
}

func TestLocalRubricPreflightAllowsDigestDetectedEdit(t *testing.T) {
	ec, env, service, cfg, dir := validationFixture(t)
	before := `{"type":"rubric","dimensions":[{"id":"clarity","weight":5}],"pass_threshold":0.6}`
	after := `{"type":"rubric","dimensions":[{"id":"clarity","weight":5}]}`
	decl := project.EvaluatorDecl{
		Name: "quality", Source: "quality.json", SupportedEvaluationLevels: []string{"conversation"},
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, decl.Source), []byte(after), 0o600))
	env.state[project.FingerprintKey("evaluator", decl.Name)] = project.FingerprintBytes([]byte(before))
	service.definition = `{"name":"quality","version":"1","supported_evaluation_levels":["turn"],` +
		`"definition":` + before + `}`
	cfg.Evaluators = []project.EvaluatorDecl{decl}
	cfg.Evals[0].Evaluators = evalcore.EvaluatorList{{Evaluator: decl.Name}}
	cfg.Evals[0].EvaluationLevel = project.EvaluationLevelConversation
	assert.True(t, sameDefinition([]byte(service.definition), []byte(`{"definition":`+after+`}`)))
	require.NoError(t, (&evalReconciler{ec: ec}).Validate(t.Context(), cfg, dir),
		"the recorded digest proves a deletion edit that will publish the authored levels")
	assert.Empty(t, env.config)
}
