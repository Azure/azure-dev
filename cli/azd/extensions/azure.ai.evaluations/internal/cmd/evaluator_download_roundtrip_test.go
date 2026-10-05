// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type evaluatorRoundTripService struct {
	mu        sync.Mutex
	document  json.RawMessage
	published []json.RawMessage
}

func evaluatorRoundTripContext(t *testing.T) (*evalContext, *evaluatorRoundTripService) {
	t.Helper()
	service := &evaluatorRoundTripService{document: json.RawMessage(downloadedEvaluator)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		service.mu.Lock()
		defer service.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		version := strconv.Itoa(3 + len(service.published))
		switch r.Method {
		case http.MethodGet:
			if strings.HasSuffix(r.URL.Path, "/versions") {
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"value": []any{map[string]string{"name": "quality", "version": version}},
				}))
			} else {
				_, err := w.Write(service.document)
				assert.NoError(t, err)
			}
		case http.MethodPost:
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); !assert.NoError(t, err) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			raw, err := json.Marshal(body)
			assert.NoError(t, err)
			service.published = append(service.published, raw)
			body["version"] = json.RawMessage(strconv.Quote(strconv.Itoa(3 + len(service.published))))
			service.document, err = json.Marshal(body)
			assert.NoError(t, err)
			_, err = w.Write(service.document)
			assert.NoError(t, err)
		default:
			t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	env := &testEnvServer{state: map[string]string{}}
	return &evalContext{
		evalClient: eval_api.NewEvalClientFromPipeline(server.URL, pipeline),
		azdClient:  newTestAzdClient(t, env),
		envName:    "test",
	}, service
}

func (s *evaluatorRoundTripService) publishedBody(t *testing.T, count int) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.published, count)
	if count == 0 {
		return nil
	}
	return decodeBody(t, s.published[count-1])
}

func downloadEditableEvaluator(t *testing.T, ec *evalContext) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "quality.json")
	action := &evaluatorDownloadAction{
		cmd: evaluatorDownloadCmd(t), name: "quality", version: "3", outFile: path,
	}
	require.NoError(t, action.download(t.Context(), ec))
	return path
}

func editDownloadedThreshold(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	edited := strings.Replace(string(raw), `"pass_threshold": 0.6`, `"pass_threshold": 0.7`, 1)
	require.NotEqual(t, string(raw), edited)
	require.NoError(t, os.WriteFile(path, []byte(edited), 0o600))
	return []byte(edited)
}

func TestEvaluatorDownloadRoundTripWithDeclaration(t *testing.T) {
	ec, service := evaluatorRoundTripContext(t)
	path := downloadEditableEvaluator(t, ec)
	service.publishedBody(t, 0)
	decl := declaredEvaluator()
	decl.Name, decl.Source = "quality", path
	r := &evalReconciler{ec: ec}

	version, published, err := r.EnsureEvaluator(t.Context(), decl, path)
	require.NoError(t, err)
	require.False(t, published, "downloading an unchanged rubric does not publish another version")
	require.Equal(t, "3", version)

	editDownloadedThreshold(t, path)
	version, published, err = r.EnsureEvaluator(t.Context(), decl, path)
	require.NoError(t, err)
	require.True(t, published)
	require.Equal(t, "4", version)
	body := service.publishedBody(t, 1)
	require.Equal(t, decl.DisplayName, body["display_name"])
	require.Equal(t, []any{"quality", "safety"}, body["categories"], "the declaration remains authoritative")
	require.Equal(t, []any{"turn", "conversation"}, body["supported_evaluation_levels"])
	definition, ok := body["definition"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 0.7, definition["pass_threshold"])
	require.Len(t, definition, 4, "unknown authored fields survive publication")
	require.Contains(t, string(service.published[0]), "9007199254740993")
	require.NotContains(t, string(service.published[0]), "definition-secret")
	require.NotContains(t, string(service.published[0]), "dimension-secret")

	ec.state = nil
	version, published, err = r.EnsureEvaluator(t.Context(), decl, path)
	require.NoError(t, err)
	require.False(t, published, "a repeat with reloaded private state must not create version 5")
	require.Equal(t, "4", version)
	service.publishedBody(t, 1)
}

func TestEvaluatorDownloadRoundTripWithStandaloneUpdate(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(strconv.FormatBool(explicit), func(t *testing.T) {
			ec, service := evaluatorRoundTripContext(t)
			path := downloadEditableEvaluator(t, ec)
			body, err := normalizeRubricBody("quality", editDownloadedThreshold(t, path))
			require.NoError(t, err)
			if explicit {
				var document map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(body, &document))
				document["display_name"] = json.RawMessage(`""`)
				document["description"] = json.RawMessage(`"Edited description"`)
				document["categories"] = json.RawMessage(`[]`)
				document["supported_evaluation_levels"] = json.RawMessage(`["conversation"]`)
				body, err = json.Marshal(document)
				require.NoError(t, err)
			}
			cmd := evaluatorDownloadCmd(t)
			cmd.Flags().String("output", "json", "")
			var out strings.Builder
			cmd.SetOut(&out)
			action := &evaluatorWriteAction{cmd: cmd, name: "quality", verb: "update"}
			require.NoError(t, action.write(t.Context(), ec, body))

			published := service.publishedBody(t, 1)
			if explicit {
				require.Equal(t, "", published["display_name"])
				require.Equal(t, "Edited description", published["description"])
				require.Empty(t, published["categories"])
				require.Equal(t, []any{"conversation"}, published["supported_evaluation_levels"])
			} else {
				require.Equal(t, "Support quality", published["display_name"])
				require.Equal(t, "Grades support conversations", published["description"])
				require.Equal(t, []any{"quality", "agents"}, published["categories"])
				require.Equal(t, []any{"turn", "conversation"}, published["supported_evaluation_levels"])
			}
			require.NotContains(t, published, "version")
			require.NotContains(t, published, "created_at")
			definition, ok := published["definition"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, 0.7, definition["pass_threshold"])
			require.Len(t, definition, 4, "unknown authored fields survive publication")
			require.Contains(t, string(service.published[0]), "9007199254740993")
			require.NotContains(t, string(service.published[0]), "definition-secret")
			require.NotContains(t, string(service.published[0]), "dimension-secret")
			require.True(t, json.Valid([]byte(out.String())), "update stdout remains one JSON document")
		})
	}
}

func TestEvaluatorUpdateRefusesUnreadableMetadata(t *testing.T) {
	for _, document := range []string{
		"", "null", "[]", "not JSON", `{}`, `{"definition":null}`,
		`{"definition":{},"supported_evaluation_levels":"conversation"}`,
	} {
		t.Run(document, func(t *testing.T) {
			ec, service := evaluatorRoundTripContext(t)
			service.document = json.RawMessage(document)
			body, err := normalizeRubricBody("quality", []byte(authoredRubric))
			require.NoError(t, err)
			action := &evaluatorWriteAction{cmd: evaluatorDownloadCmd(t), name: "quality", verb: "update"}
			require.Error(t, action.write(t.Context(), ec, body))
			service.publishedBody(t, 0)
		})
	}
}

func TestEvaluatorUpdateCancellationDoesNotPublish(t *testing.T) {
	ec, service := evaluatorRoundTripContext(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	action := &evaluatorWriteAction{cmd: evaluatorDownloadCmd(t), name: "quality", verb: "update"}
	err := action.write(ctx, ec, json.RawMessage(`{"name":"quality","definition":{"type":"rubric"}}`))
	require.ErrorIs(t, err, context.Canceled)
	service.publishedBody(t, 0)
}

func TestDownloadedRubricReconciliationRetainsMetadata(t *testing.T) {
	for _, scenario := range []struct {
		caller    string
		responses bool
	}{
		{"create", false}, {"up", false}, {"create", true}, {"up", true},
	} {
		caller, sourceType := scenario.caller, project.SourceTypeTraces
		if scenario.responses {
			sourceType = project.SourceTypeResponses
		}
		for _, override := range []string{"none", "catalog", "empty catalog lists", "document"} {
			t.Run(caller+"/"+sourceType+"/"+override, func(t *testing.T) {
				ec, _, service, cfg, dir := newCatalogPinFixture(t)
				service.latest = "3"
				service.versions = map[string]json.RawMessage{
					"3": json.RawMessage(strings.ReplaceAll(downloadedEvaluator, `"quality"`, `"custom"`)),
				}
				path := filepath.Join(dir, "custom.json")
				action := &evaluatorDownloadAction{
					cmd: evaluatorDownloadCmd(t), name: "custom", version: "3", outFile: path,
				}
				require.NoError(t, action.download(t.Context(), ec))
				cfg.Evaluators[0] = project.EvaluatorDecl{Name: "custom", Source: path}
				cfg.Evals[0].EvaluationLevel = project.EvaluationLevelConversation
				if scenario.responses {
					cfg.Evals[0].Source = &project.SourceDecl{
						Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixed"}, MaxTurns: 1,
					}
				}
				first := reconcileCatalogPin(t, caller, ec, cfg, dir)
				require.Zero(t, service.publishes, "unchanged download must reuse the published version")

				edited := editDownloadedThreshold(t, path)
				wantName, wantDescription := "Support quality", "Grades support conversations"
				wantCategories := []any{"custom", "agents"}
				wantLevels := []any{"turn", "conversation"}
				switch override {
				case "catalog":
					cfg.Evaluators[0].DisplayName = "Authored catalog name"
					cfg.Evaluators[0].Categories = []string{"safety"}
					cfg.Evaluators[0].SupportedEvaluationLevels = []string{"conversation"}
					wantName, wantCategories, wantLevels = "Authored catalog name", []any{"safety"}, []any{"conversation"}
				case "empty catalog lists":
					cfg.Evaluators[0].Categories = []string{}
					cfg.Evaluators[0].SupportedEvaluationLevels = []string{}
					wantCategories, wantLevels = []any{}, []any{}
				case "document":
					body, err := normalizeRubricBody("custom", edited)
					require.NoError(t, err)
					var document map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(body, &document))
					document["display_name"] = json.RawMessage(`""`)
					document["description"] = json.RawMessage(`""`)
					document["categories"] = json.RawMessage(`[]`)
					document["supported_evaluation_levels"] = json.RawMessage(`[]`)
					edited, err = json.Marshal(document)
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(path, edited, 0o600))
					wantName, wantDescription, wantCategories, wantLevels = "", "", []any{}, []any{}
				}
				require.Equal(t, first, reconcileCatalogPin(t, caller, ec, cfg, dir))
				require.Equal(t, 1, service.publishes)
				published := decodeBody(t, service.versions["4"])
				assert.Equal(t, wantName, published["display_name"])
				assert.Equal(t, wantDescription, published["description"])
				assert.Equal(t, wantCategories, published["categories"])
				assert.Equal(t, wantLevels, published["supported_evaluation_levels"])
				assert.NotContains(t, published, "created_at")
				assert.NotContains(t, published, "agent_metadata")
				assert.Contains(t, string(service.versions["4"]), "9007199254740993",
					"unknown authored fields retain exact numbers through the editable file")
				for _, private := range []string{"internal_count", "definition-secret", "dimension-secret",
					"service-only-definition-metadata", "service-only-agent-wiring"} {
					assert.NotContains(t, string(service.versions["4"]), private)
				}
				require.Equal(t, first, reconcileCatalogPin(t, caller, ec, cfg, dir))
				assert.Equal(t, 1, service.publishes, "unchanged retry must not publish a fifth version")
				assert.Len(t, service.created, 1, "metadata inheritance must not turn latest into an authored pin")
				require.Len(t, service.evals[first].TestingCriteria, 1)
				assert.Empty(t, service.evals[first].TestingCriteria[0].EvaluatorVersion)
				if scenario.responses {
					assert.Equal(t, map[string]any{"type": "azure_ai_source", "scenario": "responses"},
						service.evals[first].DataSourceConfig)
				}
			})
		}
	}
}

func TestRubricDownloadAndCollectionDimensionEditLifecycle(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := newCatalogPinFixture(t)
			datasets, _, datasetService, _, _ := validationFixture(t)
			ec.datasetClient = datasets.datasetClient
			datasetService.dataset = true
			datasetService.registeredRows = "{\"query\":\"hello\",\"messages\":[]}\n"
			service.latest = "3"
			remote := json.RawMessage(strings.Replace(downloadedEvaluator, `"name":"quality"`, `"name":"custom"`, 1))
			service.versions = map[string]json.RawMessage{"3": remote}

			cmd := jsonCmd(t, "json")
			cmd.SetContext(t.Context())
			cmd.SetOut(io.Discard)
			job := &eval_api.GenerationJob{ID: "existing-job", Status: "succeeded", Result: remote}
			_, err := (&jobShowAction{cmd: cmd, flags: &jobFlags{path: dir}}).
				collect(t.Context(), ec, evaluatorJobs, job, io.Discard)
			require.NoError(t, err)
			catalog, err := project.OpenEvalConfig(dir)
			require.NoError(t, err)
			require.Len(t, catalog.Evaluators, 1)
			decl := catalog.Evaluators[0]
			require.Equal(t, "Support quality", decl.DisplayName)
			require.Equal(t, []string{"quality", "agents"}, decl.Categories)
			require.Equal(t, []string{"turn", "conversation"}, decl.SupportedEvaluationLevels)
			require.NotEmpty(t, decl.Source)
			collected, err := os.ReadFile(project.ResolveSource(dir, decl.Source))
			require.NoError(t, err)
			require.JSONEq(t, editableDownloadedRubric, string(collected))

			path := filepath.Join(dir, "download-v3.json")
			action := &evaluatorDownloadAction{cmd: cmd, name: "custom", version: "3", outFile: path}
			require.NoError(t, action.download(t.Context(), ec))
			downloaded, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, collected, downloaded, "generation/job-show and explicit download must use one shape")
			cfg.Evaluators = catalog.Evaluators
			cfg.Evaluators[0].Source = filepath.Base(path)
			cfg.Datasets = []project.DatasetDecl{{Name: "turn-tests", Version: "1.0"}}
			cfg.Evals[0].Source = nil
			cfg.Evals[0].Dataset = "turn-tests"
			cfg.Evals[0].EvaluationLevel = project.EvaluationLevelConversation
			require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
			first := env.stored(t, idKey("eval", cfg.Evals[0].Name))
			require.NotEmpty(t, first)
			require.Zero(t, service.publishes, "an unchanged download must not republish enriched service fields")

			edited := strings.Replace(string(downloaded), "Is it correct?", "Is it correct and complete?", 1)
			require.NotEqual(t, string(downloaded), edited)
			require.NoError(t, os.WriteFile(path, []byte(edited), 0o600))
			for range 2 {
				ec.state = nil
				require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
				assert.Equal(t, "1.0", env.stored(t, versionKey("dataset", "turn-tests")))
				assert.Equal(t, first, env.stored(t, idKey("eval", cfg.Evals[0].Name)))
				assert.Equal(t, 1, service.publishes)
				assert.Len(t, service.created, 1)
			}
			published := decodeBody(t, service.versions["4"])
			assert.Equal(t, "Support quality", published["display_name"])
			assert.Equal(t, "Grades support conversations", published["description"])
			assert.Equal(t, []any{"quality", "agents"}, published["categories"])
			assert.Equal(t, []any{"turn", "conversation"}, published["supported_evaluation_levels"])
			var body map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(service.versions["4"], &body))
			require.JSONEq(t, edited, string(body["definition"]), "only the dimension description changed")
			final, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, edited, string(final), "reconciliation must leave the local edit intact")
			for _, request := range datasetService.requests {
				assert.True(t, strings.HasPrefix(request, "GET ") ||
					request == "POST /datasets/turn-tests/versions/1.0/credentials",
					"rubric edits must not mutate datasets: %s", request)
			}
		})
	}
}

func TestEvaluatorWithoutPriorDigestComparesExactNumbers(t *testing.T) {
	for _, tc := range []struct {
		name, existing, authored string
		publish                  bool
	}{
		{"adjacent integers", "9007199254740992", "9007199254740993", true},
		{"precise decimals", "0.60000000000000001", "0.60000000000000002", true},
		{"equivalent decimal", "1", "1.0", false},
		{"equivalent exponent", "1.0", "1e0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ec, service := evaluatorRoundTripContext(t)
			definition := func(number string) string {
				return `{"type":"rubric","dimensions":[{"id":"accuracy","weight":5,"scale":{"maximum":` + number + `}}]}`
			}
			service.document = json.RawMessage(`{"name":"quality","version":"3","definition":` +
				definition(tc.existing) + `}`)
			path := filepath.Join(t.TempDir(), "quality.json")
			require.NoError(t, os.WriteFile(path, []byte(definition(tc.authored)), 0o600))
			key := project.FingerprintKey("evaluator", "quality")
			require.Empty(t, ec.privateValue(t.Context(), key))
			reconciler := &evalReconciler{ec: ec}
			decl := project.EvaluatorDecl{Name: "quality", Source: path}

			version, published, err := reconciler.EnsureEvaluator(t.Context(), decl, path)
			require.NoError(t, err)
			require.Equal(t, tc.publish, published)
			if tc.publish {
				require.Equal(t, "4", version)
				service.publishedBody(t, 1)
				require.Contains(t, string(service.published[0]), tc.authored)
			} else {
				require.Equal(t, "3", version)
				service.publishedBody(t, 0)
			}
			require.NotEmpty(t, ec.privateValue(t.Context(), key))
			ec.state = nil
			repeatedVersion, published, err := reconciler.EnsureEvaluator(t.Context(), decl, path)
			require.NoError(t, err)
			require.False(t, published, "a repeat must reuse the correctly reconciled version")
			require.Equal(t, version, repeatedVersion)
		})
	}
}
