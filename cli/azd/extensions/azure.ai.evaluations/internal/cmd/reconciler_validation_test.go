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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

type validationService struct {
	mu                  sync.Mutex
	requests            []string
	status              int
	dataset             bool
	eval                bool
	failCreate          bool
	createStatus        int
	definition          string
	createCount         int
	evaluatorVersion    string
	createdRequests     []eval_api.CreateOpenAIEvalRequest
	registeredRows      string
	credentialStatus    int
	contentStatus       int
	datasetReadStatus   int
	emptyDatasetListing bool
	listedVersion       string
	afterContentRead    func()
}

func (s *validationService) serve(t *testing.T, base func() string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/credentials"):
			if s.credentialStatus != 0 {
				w.WriteHeader(s.credentialStatus)
				return
			}
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"blobReferenceForConsumption": map[string]any{
					"credential": map[string]any{"sasUri": base() + "/registered.jsonl"},
				},
			}))
		case r.URL.Path == "/registered.jsonl":
			if s.contentStatus != 0 {
				w.WriteHeader(s.contentStatus)
				return
			}
			_, err := w.Write([]byte(s.registeredRows))
			assert.NoError(t, err)
			if s.afterContentRead != nil {
				s.afterContentRead()
			}
		case strings.Contains(r.URL.Path, "/evaluators/"):
			if s.status != http.StatusOK {
				w.WriteHeader(s.status)
				_, _ = w.Write([]byte(`{"error":{"code":"ReferenceUnavailable"}}`))
				return
			}
			if strings.HasSuffix(r.URL.Path, "/versions") {
				version := s.evaluatorVersion
				if version == "" {
					version = "1"
				}
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"value": []map[string]string{{"name": "builtin.valid", "version": version}},
				}))
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
				if s.dataset && !s.emptyDatasetListing {
					version := s.listedVersion
					if version == "" {
						version = "1.0"
					}
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
						"value": []map[string]string{{"name": "turn-tests", "version": version}},
					}))
				} else {
					_, _ = w.Write([]byte(`{"value":[]}`))
				}
			} else if r.Method == http.MethodGet && s.datasetReadStatus != 0 {
				w.WriteHeader(s.datasetReadStatus)
			} else if s.dataset {
				version := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]string{"name": "turn-tests", "version": version}))
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/evals"):
			var request eval_api.CreateOpenAIEvalRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			s.createdRequests = append(s.createdRequests, request)
			s.createCount++
			if s.createStatus != 0 {
				w.WriteHeader(s.createStatus)
				_, _ = w.Write([]byte(`{"error":{"code":"CreateRefused"}}`))
				return
			}
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

func TestCreatePartialJSONPreservesRemediation(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			service.createStatus = status
			cmd := jsonCmd(t, "json")
			cmd.SetContext(t.Context())
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.RunE = func(*cobra.Command, []string) error {
				return (&evalCreateAction{cmd: cmd}).create(ec, cfg, &cfg.Evals[0], filepath.Join(dir, "azure.yaml"))
			}
			priorExit := exitProcess
			exitCode := 0
			exitProcess = func(code int) { exitCode = code }
			t.Cleanup(func() { exitProcess = priorExit })
			reportFailuresAsJSON(cmd)

			err := cmd.RunE(cmd, nil)
			require.Error(t, err)
			require.Equal(t, 1, exitCode)
			wantSuggestion := azdext.ErrorSuggestion(err)
			if status == http.StatusForbidden {
				require.NotEmpty(t, wantSuggestion)
			} else {
				require.Empty(t, wantSuggestion)
			}
			var result struct {
				Status    string               `json:"status"`
				Name      string               `json:"name"`
				Artifacts []reconciledArtifact `json:"artifacts"`
				Error     jsonErrorBody        `json:"error"`
				Recovery  string               `json:"recovery_command"`
			}
			decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
			require.NoError(t, decoder.Decode(&result))
			require.ErrorIs(t, decoder.Decode(new(any)), io.EOF, "the partial result must remain the only JSON document")
			assert.Equal(t, "failed", result.Status)
			assert.Equal(t, cfg.Evals[0].Name, result.Name)
			assert.Equal(t, []reconciledArtifact{{"dataset", "turn-tests", "1.0", true}}, result.Artifacts)
			if status == http.StatusForbidden {
				assert.Equal(t, err.Error(), result.Error.Message)
				assert.Equal(t, exterrors.CodeAuthFailed, result.Error.Code)
			} else {
				// ADO 5572140: the JSON message strips the internal service
				// endpoint a service refusal would otherwise carry; stderr
				// (asserted below) keeps the full diagnostic for a human.
				assert.Equal(t, jsonMessage(err), result.Error.Message)
				assert.NotEqual(t, err.Error(), result.Error.Message)
				assert.NotContains(t, result.Error.Message, "127.0.0.1")
				assert.Contains(t, err.Error(), "127.0.0.1")
				assert.Equal(t, "CreateRefused", result.Error.Code)
			}
			assert.Equal(t, wantSuggestion, result.Error.Suggestion)
			assert.Contains(t, result.Recovery, "azd ai eval create confirm-unknown-evaluator --from-file")
			assert.Contains(t, stderr.String(), err.Error())
			assert.Equal(t, "1.0", env.stored(t, versionKey("dataset", "turn-tests")))
			assert.Empty(t, env.stored(t, idKey("eval", cfg.Evals[0].Name)))
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(out.Bytes(), &fields))
			var errorFields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(fields["error"], &errorFields))
			if wantSuggestion == "" {
				assert.NotContains(t, errorFields, "suggestion")
			}

			service.createStatus = 0
			require.NoError(t, cmd.RunE(cmd, nil))
			require.NoError(t, cmd.RunE(cmd, nil))
			uploads := 0
			for _, request := range service.requests {
				if strings.Contains(request, "startPendingUpload") {
					uploads++
				}
				assert.NotContains(t, request, "DELETE ")
			}
			assert.Equal(t, 1, uploads, "a retained dependency must not be republished")
			assert.Equal(t, 2, service.createCount, "one failed create and one successful create")
		})
	}
}

func TestPartialJSONMatchesOrdinaryRemediation(t *testing.T) {
	for _, cause := range []error{
		errors.New("plain failure"),
		&azdext.LocalError{Message: "invalid input", Suggestion: "Correct the named input."},
		fmt.Errorf("wrapped: %w", &azdext.LocalError{Message: "invalid input", Suggestion: "Correct the named input."}),
		errors.Join(errors.New("other failure"),
			&azdext.ServiceError{Message: "service refused", Suggestion: "Check project permissions."}),
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			cmd := jsonCmd(t, "json")
			var ordinary, partial bytes.Buffer
			cmd.SetOut(&ordinary)
			require.ErrorIs(t, failAs(cmd, cause), cause)
			cmd.SetOut(&partial)
			require.NoError(t, reportCreatePartial(cmd, &evalContext{}, "quality", "azure.eval.yaml",
				[]reconciledArtifact{{"evaluator", "judge", "2", false}}, cause))
			var ordinaryDoc, partialDoc jsonError
			require.NoError(t, json.Unmarshal(ordinary.Bytes(), &ordinaryDoc))
			require.NoError(t, json.Unmarshal(partial.Bytes(), &partialDoc))
			assert.Equal(t, ordinaryDoc.Error, partialDoc.Error)

			raw, err := json.Marshal(generationDocument([]generationOutcome{{
				plan: generationPlan{Kind: generateKindEvaluator}, err: cause,
				ref: &project.ArtifactRef{Name: "judge", Source: "judge.json", Version: "2"},
			}}))
			require.NoError(t, err)
			var generated map[string]generationResult
			require.NoError(t, json.Unmarshal(raw, &generated))
			require.Contains(t, generated, "evaluator")
			assert.Equal(t, "catalog_failed", generated["evaluator"].Status)
			assert.Equal(t, ordinaryDoc.Error.Message, generated["evaluator"].Error)
			assert.Equal(t, ordinaryDoc.Error.Suggestion, generated["evaluator"].Suggestion)
			require.NotNil(t, generated["evaluator"].ArtifactRef)
			assert.Equal(t, "2", generated["evaluator"].Version)
		})
	}
}

func TestPartialJSONSuggestionsDoNotDiscloseURLCredentials(t *testing.T) {
	const suggestion = "Inspect https://fixture-user:fixture-password@example.test/remediation" +
		"?sig=fixture-signature#fixture-fragment and retry."
	const safeSuggestion = "Inspect https://example.test/remediation and retry."
	checkPartialJSONErrorRedaction(t, "safe validation failure", "safe validation failure", suggestion, safeSuggestion)
}

func TestPartialJSONSuggestionsRedactWhitespaceSeparatedQueryValues(t *testing.T) {
	for _, separator := range []string{" ", "\t", "\n", "\r\n"} {
		t.Run(separator, func(t *testing.T) {
			checkPartialJSONErrorRedaction(t, "safe validation failure", "safe validation failure",
				"Inspect https://example.test/remediation?sig="+separator+"fixture-signature and retry.",
				"Inspect <redacted-url> and retry.")
		})
	}
}

func TestPartialJSONMessagesDoNotDiscloseURLCredentials(t *testing.T) {
	for _, tc := range []struct{ name, message, safeMessage string }{
		{
			"userinfo query fragment",
			"Download https://fixture-user:fixture-password@example.test/artifact" +
				"?sig=fixture-signature#fixture-fragment failed.",
			"Download https://example.test/artifact failed.",
		},
		{
			"malformed URL",
			"Download https:/fixture-user:fixture-password@example.test/artifact?sig=fixture-signature failed.",
			"Download <redacted-url> failed.",
		},
		{
			"whitespace query",
			"Download https://example.test/artifact?sig= \tfixture-signature failed.",
			"Download <redacted-url> failed.",
		},
		{"plain message", "The service rejected the input.", "The service rejected the input."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const suggestion = "Check the project configuration and retry."
			checkPartialJSONErrorRedaction(t, tc.message, tc.safeMessage, suggestion, suggestion)
		})
	}
}

func checkPartialJSONErrorRedaction(t *testing.T, message, safeMessage, suggestion, safeSuggestion string) {
	t.Helper()
	for _, surface := range []string{"create", "generation"} {
		t.Run(surface, func(t *testing.T) {
			original := &azdext.LocalError{Message: message, Suggestion: suggestion}
			cause := fmt.Errorf("wrapped: %w", original)
			wantMessage := "wrapped: " + safeMessage
			cmd := jsonCmd(t, "json")
			cmd.SetContext(t.Context())
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.RunE = func(cmd *cobra.Command, _ []string) error {
				if surface == "create" {
					if err := reportCreatePartial(cmd, &evalContext{}, "quality", "azure.eval.yaml",
						[]reconciledArtifact{{"dataset", "retained", "2", true}}, cause); err != nil {
						return err
					}
				} else {
					if err := emitJSON(cmd.OutOrStdout(), generationDocument([]generationOutcome{
						{
							plan: generationPlan{Kind: generateKindDataset},
							ref:  &project.ArtifactRef{Name: "retained", Source: "rows.jsonl", Version: "2"},
						},
						{
							plan:     generationPlan{Kind: generateKindEvaluator},
							err:      cause,
							report:   generationReport{jobID: "evaluator-job"},
							recovery: "azd ai eval job show evaluator-job --evaluator",
						},
					})); err != nil {
						return err
					}
				}
				return cause
			}
			priorExit := exitProcess
			exitCode := 0
			exitProcess = func(code int) { exitCode = code }
			t.Cleanup(func() { exitProcess = priorExit })
			reportFailuresAsJSON(cmd)
			require.ErrorIs(t, cmd.RunE(cmd, nil), cause)
			assert.Equal(t, 1, exitCode)
			assert.Equal(t, message, original.Message, "redaction must not mutate the original error")
			assert.Equal(t, suggestion, original.Suggestion, "redaction must not mutate the original error")

			decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
			if surface == "create" {
				var result struct {
					Status    string               `json:"status"`
					Artifacts []reconciledArtifact `json:"artifacts"`
					Error     jsonErrorBody        `json:"error"`
					Recovery  string               `json:"recovery_command"`
				}
				require.NoError(t, decoder.Decode(&result))
				assert.Equal(t, "failed", result.Status)
				assert.Equal(t, []reconciledArtifact{{"dataset", "retained", "2", true}}, result.Artifacts)
				assert.Equal(t, wantMessage, result.Error.Message)
				assert.Equal(t, safeSuggestion, result.Error.Suggestion)
				assert.Contains(t, result.Recovery, "azd ai eval create quality --from-file")
			} else {
				var result map[string]generationResult
				require.NoError(t, decoder.Decode(&result))
				require.Contains(t, result, "dataset")
				require.Contains(t, result, "evaluator")
				assert.Equal(t, "succeeded", result["dataset"].Status)
				require.NotNil(t, result["dataset"].ArtifactRef)
				assert.Equal(t, "retained", result["dataset"].Name)
				assert.Equal(t, "2", result["dataset"].Version)
				assert.Equal(t, "failed", result["evaluator"].Status)
				assert.Equal(t, wantMessage, result["evaluator"].Error)
				assert.Equal(t, safeSuggestion, result["evaluator"].Suggestion)
				assert.Equal(t, "evaluator-job", result["evaluator"].JobID)
				assert.Equal(t, "azd ai eval job show evaluator-job --evaluator", result["evaluator"].Recovery)
				assert.NotEmpty(t, result["evaluator"].RetryGuidance)
			}
			require.ErrorIs(t, decoder.Decode(new(any)), io.EOF, "retain exactly one partial JSON document")
			assert.Contains(t, stderr.String(), wantMessage)
			for _, sensitive := range []string{
				"fixture-user", "fixture-password", "fixture-signature", "fixture-fragment", "sig=",
			} {
				assert.NotContains(t, out.String(), sensitive)
				assert.NotContains(t, stderr.String(), sensitive)
			}
		})
	}
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
