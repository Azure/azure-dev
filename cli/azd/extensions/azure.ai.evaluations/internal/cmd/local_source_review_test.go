// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
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

func runLocalCreate(t *testing.T, ec *evalContext, dir, name string) error {
	t.Helper()
	cmd := newEvalCreateCommand()
	cmd.SetContext(t.Context())
	cmd.Flags().String("output", "json", "")
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(io.Discard)
	action := &evalCreateAction{
		cmd: cmd, name: name,
		flags: &evalCreateFlags{fromFile: filepath.Join(dir, "azure.eval.yaml")},
		newContext: func(context.Context, string) (*evalContext, error) {
			return ec, nil
		},
	}
	return action.Run()
}

func typedLocalContract() *eval_api.EvaluatorSummary {
	return &eval_api.EvaluatorSummary{
		Name: "builtin.typed",
		Definition: &eval_api.EvaluatorContract{DataSchema: &eval_api.JSONSchema{
			Type: "object", Required: []string{"n", "tags", "obj", "maybe"},
			Properties: map[string]any{
				"n":    map[string]any{"type": "integer", "minimum": 0},
				"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1},
				"obj": map[string]any{
					"type": "object", "required": []string{"active"},
					"properties":           map[string]any{"active": map[string]any{"type": "boolean"}},
					"additionalProperties": false,
				},
				"maybe": map[string]any{"type": []string{"string", "null"}},
			},
		}},
	}
}

func typedLocalItemSchema() map[string]any {
	properties := typedLocalContract().DataSchema().Properties
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": properties["n"], "labels": properties["tags"], "details": properties["obj"],
			"optional_value": properties["maybe"],
		},
		"required": []string{"count", "details", "labels", "optional_value"},
	}
}

func TestExplicitLocalPublishedTypesReachCreateAndRun(t *testing.T) {
	const valid = `{"count":9007199254740993,"labels":["ok"],"details":{"active":true},"optional_value":null}`
	mapping := map[string]string{
		"n": "{{item.count}}", "tags": "{{item.labels}}", "obj": "{{item.details}}", "maybe": "{{item.optional_value}}",
	}
	for _, operation := range []string{"create", "run"} {
		for _, tc := range []struct {
			name string
			tail string
		}{
			{name: "valid", tail: valid},
			{name: "string is not integer", tail: strings.Replace(valid, "9007199254740993", `"bad"`, 1)},
			{name: "string is not array", tail: strings.Replace(valid, `["ok"]`, `"bad"`, 1)},
			{name: "nested object constraint", tail: strings.Replace(valid, `"active":true`, `"active":"bad"`, 1)},
			{name: "nullable still rejects boolean", tail: strings.Replace(valid, `"optional_value":null`,
				`"optional_value":true`, 1)},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				dir := localSourceConfig(t, valid+"\n"+tc.tail+"\n", 1)
				editLocalSourceConfig(t, dir, func(eval map[string]any) {
					eval["evaluators"] = []any{map[string]any{"evaluator": "builtin.typed", "data_mapping": mapping}}
				})
				ec, requests := localSourceContext(t, func(definition map[string]any) {
					definition["data_source_config"] = map[string]any{
						"type": "custom", "item_schema": typedLocalItemSchema(),
					}
					definition["testing_criteria"] = []any{map[string]any{"name": "typed", "data_mapping": mapping}}
				})
				ec.schemas = map[string]*eval_api.EvaluatorSummary{"builtin.typed": typedLocalContract()}
				var err error
				if operation == "create" {
					ec.state = map[string]string{}
					err = runLocalCreate(t, ec, dir, "local-quality")
				} else {
					_, err = startLocalSource(t, ec, dir, "local-quality", nil)
				}
				recorded := recordedIdentityRequests(requests)
				if tc.name != "valid" {
					require.ErrorContains(t, err, "local row 2")
					assert.Empty(t, recorded, "wrong types beyond the cap must fail before submission or stored-schema GET")
					return
				}
				require.NoError(t, err)
				posted := recorded[len(recorded)-1]
				assert.Equal(t, http.MethodPost, posted.method)
				if operation == "create" {
					require.Len(t, recorded, 1)
					var request eval_api.CreateOpenAIEvalRequest
					require.NoError(t, json.Unmarshal(posted.body, &request))
					expected, err := json.Marshal(typedLocalItemSchema())
					require.NoError(t, err)
					actual, err := json.Marshal(request.DataSourceConfig.ItemSchema)
					require.NoError(t, err)
					assert.JSONEq(t, string(expected), string(actual))
					assert.Equal(t, mapping, request.TestingCriteria[0].DataMapping)
				} else {
					require.Len(t, recorded, 2)
					assert.Contains(t, string(posted.body), `"count":9007199254740993`)
					var request eval_api.CreateOpenAIEvalRunRequest
					require.NoError(t, json.Unmarshal(posted.body, &request))
					require.Len(t, request.DataSource.Source.Content, 1)
				}
			})
		}
	}
}

func localPublicationConfig(t *testing.T, badTail, mixed bool) (string, map[string]any) {
	t.Helper()
	rows := "{\"query\":\"old\",\"context\":\"new input\"}\n"
	if badTail {
		rows += "{}\n"
	}
	dir := localSourceConfig(t, rows, 1)
	path := filepath.Join(dir, "azure.eval.yaml")
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var config map[string]any
	require.NoError(t, json.Unmarshal(body, &config))
	var definition map[string]any
	require.NoError(t, json.Unmarshal([]byte(authoredRubric), &definition))
	config["evaluators"] = []any{map[string]any{"name": "quality-custom", "definition": definition}}
	local := map[string]any{
		"name": "local-quality", "source": map[string]any{"type": "local", "file": "./local rows.jsonl"},
		"max_samples": 1, "evaluators": []any{map[string]any{"evaluator": "quality-custom"}},
	}
	evals := []any{local}
	if mixed {
		evals = append(evals, map[string]any{
			"name": "remote-quality", "source": map[string]any{"type": "responses", "response_ids": []any{"response"}},
			"evaluators": []any{map[string]any{"evaluator": "quality-custom"}},
		})
	}
	config["evals"] = evals
	body, err = json.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	return dir, config
}

func localPublicationContext(
	t *testing.T, existing bool,
) (*evalContext, <-chan identityRequest, *localSourceEnv) {
	t.Helper()
	requests := make(chan identityRequest, 40)
	published := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		requests <- identityRequest{r.Method, r.URL.Path, body}
		w.Header().Set("Content-Type", "application/json")
		contract := func() eval_api.EvaluatorSummary {
			field := "query"
			if published {
				field = "context"
			}
			return eval_api.EvaluatorSummary{Name: "quality-custom", Definition: &eval_api.EvaluatorContract{
				DataSchema: &eval_api.JSONSchema{Type: "object",
					Properties: map[string]any{field: map[string]any{"type": "string"}}},
			}}
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/evaluators":
			values := []eval_api.EvaluatorSummary{{Name: "builtin.unrelated"}}
			if published || existing {
				values = append(values, contract())
			}
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"value": values}))
		case r.Method == http.MethodPost && r.URL.Path == "/evaluators/quality-custom/versions":
			published = true
			_, _ = io.WriteString(w, `{"name":"quality-custom","version":"2"}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/evaluators/quality-custom/versions"):
			if !existing && !published {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			version := "1"
			if published {
				version = "2"
			}
			if strings.HasSuffix(r.URL.Path, "/versions") {
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"value": []any{
					map[string]any{"name": "quality-custom", "version": version},
				}}))
			} else {
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"name": "quality-custom", "version": version,
					"definition": map[string]any{"type": "rubric", "dimensions": []any{map[string]any{"id": "old"}}},
				}))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/evals":
			var request eval_api.CreateOpenAIEvalRequest
			assert.NoError(t, json.Unmarshal(body, &request))
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]string{"id": "eval_" + request.Name}))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	ec := evalContextFor(server)
	env := &localSourceEnv{testEnvServer: testEnvServer{
		config: map[string][]byte{privateStatePath: []byte("{}")},
	}}
	ec.azdClient = newTestAzdClient(t, env)
	ec.envName, ec.rootKnown = "test", true
	ec.state = map[string]string{}
	return ec, requests, env
}

func TestExplicitLocalCreateActionPreflightPrecedesEvaluatorPublish(t *testing.T) {
	for _, badTail := range []bool{false, true} {
		t.Run(fmt.Sprint(badTail), func(t *testing.T) {
			dir, _ := localPublicationConfig(t, badTail, false)
			ec, requests, env := localPublicationContext(t, false)
			err := runLocalCreate(t, ec, dir, "local-quality")
			recorded := recordedIdentityRequests(requests)
			if badTail {
				require.ErrorContains(t, err, "empty object")
				assert.Empty(t, recorded)
				assert.Zero(t, env.writes)
				assert.Equal(t, []byte("{}"), env.config[privateStatePath])
				return
			}
			require.NoError(t, err)
			posts := 0
			for _, request := range recorded {
				if request.method == http.MethodPost {
					posts++
				}
				if request.method == http.MethodPost && request.path == "/openai/v1/evals" {
					var created eval_api.CreateOpenAIEvalRequest
					require.NoError(t, json.Unmarshal(request.body, &created))
					require.Len(t, created.TestingCriteria, 1)
					assert.Equal(t, map[string]string{"context": "{{item.context}}"}, created.TestingCriteria[0].DataMapping)
				}
			}

			assert.Equal(t, 2, posts, "the control proves the same action and fixture really can publish")
			assert.Positive(t, env.writes)
		})
	}
}

func TestExplicitLocalSharedColumnKeepsEveryConstraint(t *testing.T) {
	for _, value := range []int{-1, 5, 11} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			dir := localSourceConfig(t, fmt.Sprintf("{\"count\":%d}\n", value), 0)
			editLocalSourceConfig(t, dir, func(eval map[string]any) {
				eval["evaluators"] = []any{
					map[string]any{"evaluator": "builtin.lower", "data_mapping": map[string]string{"n": "{{item.count}}"}},
					map[string]any{"evaluator": "builtin.upper", "data_mapping": map[string]string{"n": "{{item.count}}"}},
				}
			})
			ec, requests := localSourceContext(t)
			ec.state = map[string]string{}
			ec.schemas = map[string]*eval_api.EvaluatorSummary{}
			for name, constraint := range map[string]map[string]any{
				"builtin.lower": {"type": "integer", "minimum": 0},
				"builtin.upper": {"type": "integer", "maximum": 10},
			} {
				ec.schemas[name] = &eval_api.EvaluatorSummary{Name: name,
					Definition: &eval_api.EvaluatorContract{DataSchema: &eval_api.JSONSchema{
						Properties: map[string]any{"n": constraint}, Required: []string{"n"},
					}},
				}
			}
			err := runLocalCreate(t, ec, dir, "local-quality")
			recorded := recordedIdentityRequests(requests)
			if value != 5 {
				require.ErrorContains(t, err, "local row 1")
				assert.Empty(t, recorded)
				return
			}
			require.NoError(t, err)
			require.Len(t, recorded, 1)
			var created eval_api.CreateOpenAIEvalRequest
			require.NoError(t, json.Unmarshal(recorded[0].body, &created))
			schema, err := json.Marshal(created.DataSourceConfig.ItemSchema)
			require.NoError(t, err)
			assert.JSONEq(t, `{"type":"object","required":["count"],"properties":{"count":{"allOf":[`+
				`{"type":"integer","minimum":0},{"type":"integer","maximum":10}]}}}`, string(schema))
		})
	}
}

func TestExplicitLocalDeployRefreshesPublishedEvaluatorContracts(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			root, config := localPublicationConfig(t, false, true)
			ec, requests, _ := localPublicationContext(t, existing)
			client := projectServingClient(t, &azdext.ProjectConfig{Path: root})
			provider := project.NewEvalServiceTargetProvider(client,
				func(context.Context, string) (project.Reconciler, error) {
					return &evalReconciler{ec: ec}, nil
				})
			service := &azdext.ServiceConfig{
				Name: "evals", Host: project.EvalHost, AdditionalProperties: mustStruct(t, config),
			}
			_, err := provider.Deploy(t.Context(), service, nil, nil, nil)
			require.NoError(t, err)
			created := map[string]eval_api.CreateOpenAIEvalRequest{}
			for _, request := range recordedIdentityRequests(requests) {
				if request.method == http.MethodPost && request.path == "/openai/v1/evals" {
					var body eval_api.CreateOpenAIEvalRequest
					require.NoError(t, json.Unmarshal(request.body, &body))
					created[body.Name] = body
				}
			}
			require.Len(t, created, 2)
			for _, name := range []string{"local-quality", "remote-quality"} {
				require.Len(t, created[name].TestingCriteria, 1)
				assert.Equal(t, map[string]string{"context": "{{item.context}}"},
					created[name].TestingCriteria[0].DataMapping)
			}
		})
	}
}
