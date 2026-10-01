// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func localSelectedContractContext(t *testing.T, pinStatus int) (*evalContext, <-chan identityRequest) {
	t.Helper()
	requests := make(chan identityRequest, 30)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		requests <- identityRequest{r.Method, r.URL.Path, body}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/evaluators/custom.valid/versions/9":
			_, _ = io.WriteString(w, `{"name":"custom.valid","version":"9","definition":{"data_schema":`+
				`{"type":"object","properties":{"n":{"type":"string"}},"required":["n"]}}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/evaluators/custom.valid/versions/7":
			if pinStatus != 0 {
				w.WriteHeader(pinStatus)
				return
			}
			_, _ = io.WriteString(w, `{"name":"custom.valid","version":"7","definition":{"data_schema":`+
				`{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/openai/v1/evals/eval_local":
			_, _ = io.WriteString(w, `{"id":"eval_local","data_source_config":{"item_schema":`+
				`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}},`+
				`"testing_criteria":[{"evaluator_name":"custom.valid","evaluator_version":"7",`+
				`"data_mapping":{"n":"{{item.count}}"}}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/evals":
			_, _ = io.WriteString(w, `{"id":"eval_local"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/evals/eval_local/runs":
			_, _ = io.WriteString(w, `{"id":"evalrun_local","status":"queued"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	ec := evalContextFor(server)
	ec.state = map[string]string{idKey("eval", "local-quality"): "eval_local"}
	ec.schemas = map[string]*eval_api.EvaluatorSummary{
		"custom.valid": {Name: "custom.valid", Version: "9",
			Definition: &eval_api.EvaluatorContract{DataSchema: &eval_api.JSONSchema{
				Type: "object", Required: []string{"n"},
				Properties: map[string]any{"n": map[string]any{"type": "string"}},
			}},
		},
	}
	return ec, requests
}

func writeLocalContractConfig(t *testing.T, dir string, cfg *project.EvalConfig) {
	t.Helper()
	body, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.eval.yaml"), body, 0o600))
}

func deployLocalContractConfig(t *testing.T, ec *evalContext, dir string, cfg *project.EvalConfig) error {
	t.Helper()
	body, err := json.Marshal(cfg)
	require.NoError(t, err)
	var values map[string]any
	require.NoError(t, json.Unmarshal(body, &values))
	client := projectServingClient(t, &azdext.ProjectConfig{Path: dir})
	provider := project.NewEvalServiceTargetProvider(client, func(context.Context, string) (project.Reconciler, error) {
		return &evalReconciler{ec: ec}, nil
	})
	_, err = provider.Deploy(t.Context(), &azdext.ServiceConfig{
		Name: "evals", Host: project.EvalHost, AdditionalProperties: mustStruct(t, values),
	}, nil, nil, nil)
	return err
}

func TestExplicitLocalSelectedEvaluatorPinsAcrossActions(t *testing.T) {
	for _, operation := range []string{"create", "up", "run"} {
		for _, catalogPin := range []bool{false, true} {
			t.Run(operation+"/"+fmt.Sprint(catalogPin), func(t *testing.T) {
				dir := localSourceConfig(t, "{\"count\":9007199254740993}\n", 0)
				cfg, err := project.OpenEvalConfig(dir)
				require.NoError(t, err)
				ref := &cfg.Evals[0].Evaluators[0]
				ref.Evaluator = "custom.valid"
				ref.DataMapping = map[string]string{"n": "{{item.count}}"}
				cfg.Evaluators = []project.EvaluatorDecl{{Name: "custom.valid"}}
				if catalogPin {
					cfg.Evaluators[0].Version = "7"
				} else {
					ref.Version = "7"
					cfg.Evaluators[0].Version = "9"
				}
				writeLocalContractConfig(t, dir, cfg)
				ec, requests := localSelectedContractContext(t, 0)
				latest := ec.schemas["custom.valid"]
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
				require.NoError(t, err)
				recorded := recordedIdentityRequests(requests)
				require.NotEmpty(t, recorded)
				assert.Equal(t, "/evaluators/custom.valid/versions/7", recorded[0].path)
				var posts []identityRequest
				for _, req := range recorded {
					assert.NotContains(t, req.path, "/datasets")
					if req.method == http.MethodPost {
						posts = append(posts, req)
					}
				}
				require.Len(t, posts, 1)
				if operation == "run" {
					assert.Contains(t, string(posts[0].body), `"count":9007199254740993`)
				} else {
					var created eval_api.CreateOpenAIEvalRequest
					require.NoError(t, json.Unmarshal(posts[0].body, &created))
					assert.Equal(t, "7", created.TestingCriteria[0].EvaluatorVersion)
					properties, ok := created.DataSourceConfig.ItemSchema["properties"].(map[string]any)
					require.True(t, ok)
					assert.Equal(t, map[string]any{"type": "integer"}, properties["count"])
				}
				assert.Same(t, latest, ec.schemas["custom.valid"], "selected pins never overwrite the latest cache")
			})
		}
	}
}

func TestExplicitLocalPinnedEvaluatorFailureNeverFallsBack(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			dir := localSourceConfig(t, "{\"count\":\"latest accepts this\"}\n", 0)
			editLocalSourceConfig(t, dir, func(eval map[string]any) {
				eval["evaluators"] = []any{map[string]any{
					"evaluator": "custom.valid", "version": "7", "data_mapping": map[string]string{"n": "{{item.count}}"},
				}}
			})
			ec, requests := localSelectedContractContext(t, status)
			_, err := startLocalSource(t, ec, dir, "local-quality", nil)
			require.Error(t, err)
			recorded := recordedIdentityRequests(requests)
			require.Len(t, recorded, 1)
			assert.Equal(t, http.MethodGet, recorded[0].method)
			assert.Equal(t, "/evaluators/custom.valid/versions/7", recorded[0].path)
		})
	}
}

func TestExplicitLocalNewServiceConstraintCheckedAfterPublication(t *testing.T) {
	dir, _ := localPublicationConfig(t, false, false)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "local rows.jsonl"),
		[]byte("{\"query\":\"old\",\"context\":\"valid\"}\n{\"query\":\"missing future field\"}\n"), 0o600))
	ec, requests, env := localPublicationContext(t, false, &eval_api.JSONSchema{
		Type: "object", Required: []string{"context"},
		Properties: map[string]any{"context": map[string]any{"type": "string"}},
	})
	err := runLocalCreate(t, ec, dir, "local-quality")
	require.ErrorContains(t, err, "context")
	posts := 0
	for _, request := range recordedIdentityRequests(requests) {
		if request.method == http.MethodPost {
			posts++
			assert.Equal(t, "/evaluators/quality-custom/versions", request.path,
				"unknown service enrichment can follow evaluator publication, never eval or run submission")
		}
	}
	assert.Equal(t, 1, posts)
	assert.Positive(t, env.writes, "the successful evaluator publication remains recorded")
}

func TestExplicitLocalSameEvaluatorVersionsRemainDistinct(t *testing.T) {
	dir := localSourceConfig(t, "{\"count\":42,\"label\":\"text\"}\n", 0)
	editLocalSourceConfig(t, dir, func(eval map[string]any) {
		eval["evaluators"] = []any{
			map[string]any{"evaluator": "custom.valid", "version": "7", "name": "numeric",
				"data_mapping": map[string]string{"n": "{{item.count}}"}},
			map[string]any{"evaluator": "custom.valid", "version": "9", "name": "textual",
				"data_mapping": map[string]string{"n": "{{item.label}}"}},
		}
	})
	cfg, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	cfg.Evaluators = []project.EvaluatorDecl{{Name: "custom.valid", Version: "9"}}
	writeLocalContractConfig(t, dir, cfg)
	ec, requests := localSelectedContractContext(t, 0)
	ec.state = map[string]string{}
	require.NoError(t, runLocalCreate(t, ec, dir, "local-quality"))
	var created eval_api.CreateOpenAIEvalRequest
	for _, req := range recordedIdentityRequests(requests) {
		if req.method == http.MethodPost {
			require.NoError(t, json.Unmarshal(req.body, &created))
		}
	}
	require.Len(t, created.TestingCriteria, 2)
	assert.Equal(t, "7", created.TestingCriteria[0].EvaluatorVersion)
	assert.Equal(t, "9", created.TestingCriteria[1].EvaluatorVersion)
	properties, ok := created.DataSourceConfig.ItemSchema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"type": "integer"}, properties["count"])
	assert.Equal(t, map[string]any{"type": "string"}, properties["label"])
}

func TestExplicitLocalAuthoredContractPrecedesPublication(t *testing.T) {
	for _, operation := range []string{"create", "up"} {
		for _, existing := range []bool{false, true} {
			for _, file := range []bool{false, true} {
				t.Run(operation+"/"+fmt.Sprint(existing)+"/"+fmt.Sprint(file), func(t *testing.T) {
					dir, _ := localPublicationConfig(t, false, false)
					require.NoError(t, os.WriteFile(filepath.Join(dir, "local rows.jsonl"),
						[]byte("{\"query\":\"old\",\"context\":\"valid\"}\n{\"query\":\"missing new input\"}\n"), 0o600))
					cfg, err := project.OpenEvalConfig(dir)
					require.NoError(t, err)
					cfg.Evaluators[0].Definition["data_schema"] = map[string]any{
						"type": "object", "required": []string{"context"},
						"properties": map[string]any{"context": map[string]any{"type": "string"}},
					}
					if file {
						body, err := json.Marshal(cfg.Evaluators[0].Definition)
						require.NoError(t, err)
						require.NoError(t, os.WriteFile(filepath.Join(dir, "rubric.json"), body, 0o600))
						cfg.Evaluators[0].Definition = nil
						cfg.Evaluators[0].Source = "rubric.json"
					}
					writeLocalContractConfig(t, dir, cfg)
					ec, requests, env := localPublicationContext(t, existing)
					if operation == "create" {
						err = runLocalCreate(t, ec, dir, "local-quality")
					} else {
						err = deployLocalContractConfig(t, ec, dir, cfg)
					}
					require.Error(t, err)
					assert.Contains(t, err.Error(), "context",
						"reject the prospective authored contract, not an unrelated lookup")
					assert.Zero(t, env.writes, "contract failure must precede any state write")
					assert.Equal(t, []byte("{}"), env.config[privateStatePath])
					for _, req := range recordedIdentityRequests(requests) {
						assert.Equal(t, http.MethodGet, req.method,
							"no evaluator/eval publication before contract validation")
						assert.False(t, strings.Contains(req.path, "/datasets"))
					}
				})
			}
		}
	}
}
