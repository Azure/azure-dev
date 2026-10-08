// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseFirstLocalEvaluatorPublicationWithEmptyVersions(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			var mu sync.Mutex
			var requests []string
			publishes, pointReads, evalCreates := 0, 0, 0
			var created eval_api.CreateOpenAIEvalRequest
			published := `{"name":"custom","version":"1","supported_evaluation_levels":["conversation"],` +
				`"definition":{"type":"rubric","dimensions":[{"id":"clarity","weight":5}],` +
				`"data_schema":{"properties":{"messages":{"type":"array"}},"required":["messages"]},` +
				`"init_parameters":{"properties":{"model":{}},"required":["model"]}}}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/evaluators/custom/versions":
					if publishes == 0 {
						_, err := w.Write([]byte(`{"value":[]}`))
						assert.NoError(t, err)
					} else {
						_, err := w.Write([]byte(`{"value":[{"name":"custom","version":"1"}]}`))
						assert.NoError(t, err)
					}
				case r.Method == http.MethodPost && r.URL.Path == "/evaluators/custom/versions":
					publishes++
					_, err := w.Write([]byte(published))
					assert.NoError(t, err)
				case r.Method == http.MethodGet && r.URL.Path == "/evaluators/custom/versions/1":
					pointReads++
					assert.Equal(t, 1, publishes)
					_, err := w.Write([]byte(published))
					assert.NoError(t, err)
				case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/evals":
					evalCreates++
					assert.GreaterOrEqual(t, pointReads, 2)
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&created))
					_, err := w.Write([]byte(`{"id":"eval_first","name":"quality"}`))
					assert.NoError(t, err)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			env := &testEnvServer{}
			ec := evalContextFor(server)
			ec.azdClient, ec.envName = newTestAzdClient(t, env), "test"
			cfg := &project.EvalConfig{
				Evaluators: []project.EvaluatorDecl{{
					Name: "custom", SupportedEvaluationLevels: []string{"conversation"},
					Definition: map[string]any{
						"type":       "rubric",
						"dimensions": []any{map[string]any{"id": "clarity", "weight": 5}},
					},
				}},
				Evals: []project.Eval{{
					Name: "quality", EvaluationLevel: project.EvaluationLevelConversation,
					Source: &project.SourceDecl{
						Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixed"}, MaxTurns: 1,
					},
					Evaluators: evalcore.EvaluatorList{{
						Evaluator: "custom", InitializationParameters: map[string]any{"model": "judge"},
					}},
				}},
			}
			err := reconcileArtifactConfig(t, caller, ec, cfg, t.TempDir())
			t.Logf("requests=%v publishes=%d pointReads=%d evalCreates=%d error=%v",
				requests, publishes, pointReads, evalCreates, err)
			require.NoError(t, err, "a valid complete empty version listing must allow first owned-local publication")
			assert.Equal(t, 1, publishes)
			assert.Equal(t, 1, evalCreates)
			require.NotNil(t, created.DataSourceConfig)
			assert.Equal(t, "azure_ai_source", created.DataSourceConfig.Type)
			assert.Equal(t, "responses", created.DataSourceConfig.Scenario)
			require.Len(t, created.TestingCriteria, 1)
			assert.Equal(t, "{{item.messages}}", created.TestingCriteria[0].DataMapping["messages"])
			assert.Equal(t, "judge", created.TestingCriteria[0].InitializationParameters["model"])
			assert.Empty(t, created.TestingCriteria[0].EvaluatorVersion)
			assert.Equal(t, "1", env.stored(t, versionKey("evaluator", "custom")))
		})
	}
}

func TestResponseReusedContractRejectsBeforeStateMutation(t *testing.T) {
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
					cfg.Datasets = nil
					cfg.Evaluators = []project.EvaluatorDecl{decl}
					cfg.Evals[0].Dataset = ""
					cfg.Evals[0].Source = &project.SourceDecl{
						Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixed"}, MaxTurns: 1,
					}
					cfg.Evals[0].Evaluators = evalcore.EvaluatorList{{Evaluator: decl.Name}}
					cfg.Evals[0].EvaluationLevel = project.EvaluationLevelConversation

					err := reconcileArtifactConfig(t, caller, ec, cfg, dir)
					require.ErrorContains(t, err, "conversation")
					assert.Equal(t, []string{
						"GET /evaluators/quality/versions", "GET /evaluators/quality/versions/1",
					}, service.requests)
					assert.Empty(t, env.config, "incompatible reused contracts must fail before private-state writes")
					assert.Empty(t, env.values)
					assert.Zero(t, service.createCount)
					assert.Equal(t, "preserve", env.stored(t, "unrelated"))
				})
			}
		}
	}
}

func TestResponseReusedContractAcceptsPublishedLevel(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			service.definition = `{"name":"quality","version":"1","supported_evaluation_levels":["conversation"],` +
				`"definition":{"type":"rubric","dimensions":[{"id":"clarity","weight":5}]}}`
			cfg.Datasets = nil
			cfg.Evaluators = []project.EvaluatorDecl{{
				Name: "quality", SupportedEvaluationLevels: []string{"turn"},
				Definition: map[string]any{
					"type":       "rubric",
					"dimensions": []any{map[string]any{"id": "clarity", "weight": 5}},
				},
			}}
			cfg.Evals[0].Dataset = ""
			cfg.Evals[0].Source = &project.SourceDecl{
				Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixed"}, MaxTurns: 1,
			}
			cfg.Evals[0].Evaluators = evalcore.EvaluatorList{{Evaluator: "quality"}}
			cfg.Evals[0].EvaluationLevel = project.EvaluationLevelConversation

			require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
			for _, request := range service.requests {
				assert.NotEqual(t, "POST /evaluators/quality/versions", request, "the evaluator must be reused")
			}
			assert.Equal(t, 1, service.createCount)
			assert.Equal(t, "1", env.stored(t, versionKey("evaluator", "quality")))
			assert.Equal(t, "preserve", env.stored(t, "unrelated"))
		})
	}
}
