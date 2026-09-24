// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublishedEvaluatorContractUsesExactReconciledVersion(t *testing.T) {
	for _, mode := range []struct {
		caller     string
		sourceType string
	}{
		{"create", project.SourceTypeTraces},
		{"up", project.SourceTypeTraces},
		{"create", project.SourceTypeResponses},
		{"up", project.SourceTypeResponses},
	} {
		for _, failure := range []string{"", "unavailable", "malformed"} {
			t.Run(mode.caller+"/"+mode.sourceType+"/"+failure, func(t *testing.T) {
				var mu sync.Mutex
				version := "1"
				publishes, pointReads, evalCreates := 0, 0, 0
				var created eval_api.CreateOpenAIEvalRequest
				old := `{"name":"custom","version":"1","supported_evaluation_levels":["turn"],` +
					`"definition":{"type":"rubric","dimensions":[{"id":"clarity","weight":1}],` +
					`"init_parameters":{"properties":{"deployment_name":{}},"required":["deployment_name"]}}}`
				current := `{"name":"custom","version":"2","supported_evaluation_levels":["conversation"],` +
					`"definition":{"type":"rubric","dimensions":[{"id":"clarity","weight":5}],` +
					`"data_schema":{"properties":{"messages":{"type":"array"}},"required":["messages"]},` +
					`"init_parameters":{"properties":{"model":{}},"required":["model"]}}}`
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					switch {
					case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/evaluators/"):
						publishes++
						version = "2"
						_, err := w.Write([]byte(current))
						assert.NoError(t, err)
					case r.URL.Path == "/evaluators/custom/versions":
						assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
							"value": []map[string]string{{"name": "custom", "version": version}},
						}))
					case r.URL.Path == "/evaluators/custom/versions/1":
						_, err := w.Write([]byte(old))
						assert.NoError(t, err)
					case r.URL.Path == "/evaluators/custom/versions/2":
						pointReads++
						if pointReads > 1 && failure == "unavailable" {
							w.WriteHeader(http.StatusServiceUnavailable)
						} else if pointReads > 1 && failure == "malformed" {
							_, err := w.Write([]byte("null"))
							assert.NoError(t, err)
						} else {
							_, err := w.Write([]byte(current))
							assert.NoError(t, err)
						}
					case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/evals":
						assert.GreaterOrEqual(t, pointReads, 2, "read the settled contract before eval creation")
						evalCreates++
						assert.NoError(t, json.NewDecoder(r.Body).Decode(&created))
						assert.NoError(t, json.NewEncoder(w).Encode(eval_api.OpenAIEval{
							ID: "eval_published", Name: created.Name,
							TestingCriteria: created.TestingCriteria, Metadata: created.Metadata,
						}))
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusBadRequest)
					}
				}))
				t.Cleanup(server.Close)
				env := &testEnvServer{}
				pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
					&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
				stale, err := evaluatorContract(json.RawMessage(old))
				require.NoError(t, err)
				ec := &evalContext{
					evalClient: eval_api.NewEvalClientFromPipeline(server.URL, pipeline),
					azdClient:  newTestAzdClient(t, env), envName: "test",
					schemas: map[string]*eval_api.EvaluatorSummary{"custom": stale},
				}
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "custom.json"),
					[]byte(`{"type":"rubric","dimensions":[{"id":"clarity","weight":5}]}`), 0o600))
				source := &project.SourceDecl{Type: mode.sourceType, AgentName: "agent"}
				if mode.sourceType == project.SourceTypeResponses {
					source = &project.SourceDecl{
						Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixed"}, MaxTurns: 1,
					}
				}
				cfg := &project.EvalConfig{
					Evaluators: []project.EvaluatorDecl{{
						Name: "custom", Source: "custom.json", SupportedEvaluationLevels: []string{"conversation"},
					}},
					Evals: []project.Eval{{
						Name: "quality", Source: source,
						EvaluationLevel: project.EvaluationLevelConversation,
						Evaluators: evalcore.EvaluatorList{{
							Evaluator: "custom", InitializationParameters: map[string]any{"model": "judge"},
						}},
					}},
				}
				err = reconcileArtifactConfig(t, mode.caller, ec, cfg, dir)
				if failure == "" {
					require.NoError(t, err)
					require.Equal(t, 1, evalCreates)
					require.Len(t, created.TestingCriteria, 1)
					assert.Equal(t, "judge", created.TestingCriteria[0].InitializationParameters["model"])
					assert.NotContains(t, created.TestingCriteria[0].InitializationParameters, "deployment_name")
					assert.Empty(t, created.TestingCriteria[0].EvaluatorVersion, "resolved versions are not authored pins")
					assert.Equal(t, "{{item.messages}}", created.TestingCriteria[0].DataMapping["messages"])
					if mode.sourceType == project.SourceTypeResponses {
						require.NotNil(t, created.DataSourceConfig)
						assert.Equal(t, "azure_ai_source", created.DataSourceConfig.Type)
						assert.Equal(t, "responses", created.DataSourceConfig.Scenario)
					}
				} else {
					require.Error(t, err)
					assert.Zero(t, evalCreates, "a failed exact-version read must not fall back to the stale contract")
				}
				assert.Equal(t, 1, publishes)
				assert.GreaterOrEqual(t, pointReads, 2, "read the published version after propagation confirmation")
				assert.Equal(t, "2", env.stored(t, versionKey("evaluator", "custom")))
			})
		}
	}
}

func TestEffectiveCatalogPinDuplicatesFailBeforeDeployment(t *testing.T) {
	ec, env, service, cfg, dir := newCatalogPinFixture(t)
	second := cfg.Evals[0]
	second.Name = "second"
	second.Evaluators = evalcore.EvaluatorList{{Evaluator: "custom", Version: "1"}}
	cfg.Evals = append(cfg.Evals, second)
	require.NoError(t, cfg.Validate(), "the raw declarations spell the same effective pin differently")
	_, err := deployValidationFixture(t, t.Context(), ec, cfg, dir)
	require.Error(t, err)
	assert.Empty(t, service.reads)
	assert.Empty(t, service.created)
	assert.Empty(t, env.config)
	assert.Empty(t, cfg.Evals[0].Evaluators[0].Version, "normalization must not rewrite authored references")
}

func TestLocalRubricValidationAttributesTheEvaluatorOnce(t *testing.T) {
	ec, _, _, cfg, dir := validationFixture(t)
	cfg.Evaluators = []project.EvaluatorDecl{{
		Name: "local", Definition: map[string]any{
			"dimensions": []any{map[string]any{"id": "clarity", "weight": 0}},
		},
	}}
	cfg.Evals[0].Evaluators = evalcore.EvaluatorList{{Evaluator: "local"}}
	err := (&evalReconciler{ec: ec}).Validate(t.Context(), cfg, dir)
	require.Error(t, err)
	assert.Equal(t, 1, strings.Count(err.Error(), `evaluator "local":`))
	assert.Contains(t, err.Error(), "definition.dimensions[0].weight")
}
