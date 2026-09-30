// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExplicitTraceScenarioCallers(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, scenario := range []string{"traces", "traces_preview", "responses", "unknown"} {
			t.Run(caller+"/"+scenario, func(t *testing.T) {
				ec, env, service, cfg, dir := newCatalogPinFixture(t)
				cfg.Evals[0].ID = "eval_sdk"
				service.evals["eval_sdk"] = &eval_api.OpenAIEval{
					ID: "eval_sdk", Name: "sdk-owned",
					DataSourceConfig: map[string]any{"type": "azure_ai_source", "scenario": scenario},
				}
				err := reconcileArtifactConfig(t, caller, ec, cfg, dir)
				if scenario == "traces" || scenario == "traces_preview" {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "does not use")
				}
				assert.Empty(t, service.created)
				assert.Zero(t, service.publishes)
				assert.Equal(t, "sdk-owned", service.evals["eval_sdk"].Name)
				assert.Empty(t, env.config)
			})
		}
	}
}

func TestResponseRunChecksSchemaBothDirections(t *testing.T) {
	for _, mode := range []string{"trace switch", "dataset override", "response source"} {
		for _, remote := range []string{"responses", "custom", "traces", "unknown", "legacy", "read failure"} {
			t.Run(mode+"/"+remote, func(t *testing.T) {
				group := responseGroup()
				group.Name = "quality"
				if mode == "trace switch" {
					group.Source = &project.SourceDecl{Type: project.SourceTypeTraces, AgentName: "agent"}
				}
				cfg := project.EvalConfig{
					Evals: []project.Eval{group}, Datasets: []project.DatasetDecl{{Name: "golden", File: "rows.jsonl"}},
				}
				dir := t.TempDir()
				raw, err := json.Marshal(cfg)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), raw, 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(oneRow), 0o600))
				service := identityService{
					listStatus: http.StatusNotFound, getStatus: http.StatusNotFound,
					responseEval: remote == "responses",
				}
				if remote == "read failure" {
					service.evalStatus = http.StatusForbidden
				}
				if remote == "custom" {
					service.evalConfig = map[string]any{"type": "custom"}
				} else if remote == "traces" || remote == "unknown" {
					service.evalConfig = map[string]any{"type": "azure_ai_source", "scenario": remote}
				}
				ec, requests := identityRunContext(t, service)
				ec.state = map[string]string{idKey("eval", group.Name): "eval_1"}
				cmd := buildRunCommand("start", "")
				cmd.SetContext(t.Context())
				var out bytes.Buffer
				cmd.SetOut(&out)
				cmd.Flags().String("output", "json", "")
				flags := &runStartFlags{groupName: group.Name, evalPath: dir}
				if mode == "dataset override" {
					flags.datasetName = "golden"
					require.NoError(t, cmd.Flags().Set("dataset", "golden"))
				}
				err = (&runStartAction{
					cmd: cmd, flags: flags,
					newContext: func(context.Context, string) (*evalContext, error) { return ec, nil },
				}).Run()
				posts, schemaReads := 0, 0
				for _, request := range recordedIdentityRequests(requests) {
					if request.method == http.MethodGet && strings.HasSuffix(request.path, "/eval_1") {
						schemaReads++
					}
					if request.method == http.MethodPost && strings.HasSuffix(request.path, "/runs") {
						posts++
					}
					assert.NotContains(t, request.path, "/agents/")
					assert.NotContains(t, request.path, "/responses")
				}
				assert.Equal(t, 1, schemaReads)
				compatible := remote == "custom" || remote == "legacy" || (remote == "traces" && mode == "trace switch")
				if mode == "response source" {
					compatible = remote == "responses"
				}
				mismatch := !compatible
				if remote == "read failure" {
					require.ErrorContains(t, err, `reading eval "eval_1"`)
				} else if mismatch {
					require.ErrorContains(t, err, "does not use")
				} else {
					require.NoError(t, err)
				}
				if mismatch || remote == "read failure" {
					assert.Zero(t, posts)
					assert.Empty(t, out.String())
				} else {
					assert.Equal(t, 1, posts)
				}
			})
		}
	}
}

func TestResponsePreparedMappingsAndRun(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, level := range []string{"", "turn", "conversation"} {
			for _, shape := range []string{"string", "array", "union", "explicit"} {
				t.Run(caller+"/"+level+"/"+shape, func(t *testing.T) {
					ec, _, service, cfg, dir := newCatalogPinFixture(t)
					service.versions = map[string]json.RawMessage{"1": responseReviewContract(shape)}
					cfg.Evals[0].Source = &project.SourceDecl{
						Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixed"}, MaxTurns: 1,
					}
					cfg.Evals[0].EvaluationLevel = level
					if shape == "explicit" && level != "conversation" {
						cfg.Evals[0].Evaluators[0].DataMapping = map[string]string{"response": "{{item.authored_answer}}"}
					}
					id := reconcileCatalogPin(t, caller, ec, cfg, dir)
					assert.Equal(t, id, reconcileCatalogPin(t, caller, ec, cfg, dir))
					require.Len(t, service.created, 1)
					request := service.created[0]
					require.Len(t, request.TestingCriteria, 1)
					mapping := request.TestingCriteria[0].DataMapping
					if level == "conversation" {
						assert.Equal(t, "{{item.messages}}", mapping["messages"])
						assert.NotContains(t, mapping, "response")
					} else {
						want := "{{sample.output_items}}"
						if shape == "string" {
							want = "{{sample.output_text}}"
						} else if shape == "explicit" {
							want = "{{item.authored_answer}}"
						}
						assert.Equal(t, want, mapping["response"])
						assert.Equal(t, "{{item.query}}", mapping["query"])
					}
					assert.Equal(t, "1", request.TestingCriteria[0].EvaluatorVersion)
					assert.Equal(t, &eval_api.DataSourceConfig{Type: "azure_ai_source", Scenario: "responses"},
						request.DataSourceConfig)
					raw, err := json.Marshal(cfg)
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), raw, 0o600))
					cmd := jsonCmd(t, "json")
					cmd.SetContext(t.Context())
					var out bytes.Buffer
					cmd.SetOut(&out)
					fresh := *ec
					fresh.state = nil
					require.NoError(t, (&runStartAction{
						cmd: cmd, flags: &runStartFlags{groupName: cfg.Evals[0].Name, evalPath: dir},
						newContext: func(context.Context, string) (*evalContext, error) { return &fresh, nil },
					}).Run())
					require.Len(t, service.runs, 1)
					assert.Equal(t, eval_api.NewResponsesDataSource([]string{"resp_fixed"}, 1), service.runs[0].DataSource)
					assert.Equal(t, level, service.runs[0].EvaluationLevel)
					assert.Zero(t, service.publishes)
				})
			}
		}
	}
}

func responseReviewContract(shape string) json.RawMessage {
	property := `{"type":"string"}`
	if shape == "array" {
		property = `{"type":"array","items":{"type":"object"}}`
	} else if shape == "union" {
		property = `{"anyOf":[{"type":"string"},{"type":"array"}]}`
	}
	return json.RawMessage(`{"name":"custom","version":"1","definition":{"data_schema":{"properties":{` +
		`"query":{"type":"string"},"response":` + property + `,"messages":{"type":"array"}}}}}`)
}

func TestResponseBindingsStayTypedAndSourceScoped(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{}
	for _, shape := range []string{"string", "array", "union", "unspecified"} {
		contract, err := evaluatorContract(responseReviewContract(shape))
		require.NoError(t, err)
		if shape == "unspecified" {
			contract.Definition.DataSchema.Properties["response"] = map[string]any{}
		}
		schemas[shape] = contract
	}
	for _, mode := range []string{"responses", "traces", "static"} {
		for _, target := range []bool{false, true} {
			group := project.Eval{Name: "mixed", Dataset: "dataset", EvaluationLevel: "turn"}
			if target {
				group.Target = &project.Target{Type: project.TargetTypeAgent, Name: "agent"}
			}
			if mode != "static" {
				group.Dataset = ""
				group.Source = &project.SourceDecl{Type: mode}
			}
			for _, shape := range []string{"string", "array", "union", "unspecified"} {
				group.Evaluators = append(group.Evaluators, evalcore.EvaluatorRef{Evaluator: shape})
			}
			request, err := buildEvalRequest(&group, schemas, nil)
			require.NoError(t, err)
			for _, criterion := range request.TestingCriteria {
				want := "{{item.response}}"
				if mode == "responses" || (target && mode == "static") {
					want = "{{sample.output_items}}"
				}
				if mode == "responses" && criterion.EvaluatorName == "string" {
					want = "{{sample.output_text}}"
				}
				assert.Equal(t, want, criterion.DataMapping["response"])
			}
			assert.Equal(t, "{{sample.output_items}}", sampleBindings["response"], "typed bindings must not mutate defaults")
			if mode == "traces" {
				assert.False(t, request.DataSourceConfig.IncludeSampleSchema)
			}
		}
	}
}

func TestResponsePreparedMappingMigration(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, lookup := range []string{"cached", "rename"} {
			for _, tc := range []struct {
				shape, oldBinding, want string
			}{
				{"string", "{{item.response}}", "{{sample.output_text}}"},
				{"string", "{{sample.output_items}}", "{{sample.output_text}}"},
				{"string", "{{sample.output_text}}", "{{sample.output_text}}"},
				{"array", "{{sample.output_text}}", "{{sample.output_items}}"},
				{"array", "{{sample.output_items}}", "{{sample.output_items}}"},
			} {
				t.Run(caller+"/"+lookup+"/"+tc.shape+"/"+tc.oldBinding, func(t *testing.T) {
					ec, env, service, cfg, dir := newCatalogPinFixture(t)
					service.versions = map[string]json.RawMessage{"1": responseReviewContract(tc.shape)}
					cfg.Evals[0].Source = &project.SourceDecl{
						Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixed"}, MaxTurns: 1,
					}
					first := reconcileCatalogPin(t, caller, ec, cfg, dir)
					service.evals[first].TestingCriteria[0].DataMapping["response"] = tc.oldBinding
					service.evals[first].DataSourceConfig["include_sample_schema"] = true
					original, err := json.Marshal(service.evals[first])
					require.NoError(t, err)
					seedLegacyCatalogPinState(t, env, cfg.Evals[0], first)
					var state map[string]string
					require.NoError(t, json.Unmarshal(env.config[privateStatePath], &state))
					legacy := cfg.Evals[0]
					legacy.Source = nil
					definition, err := project.FingerprintDefinition(legacy)
					require.NoError(t, err)
					state[project.FingerprintKey("eval", cfg.Evals[0].Name)] = fingerprintEra + definition
					env.config[privateStatePath], err = json.Marshal(state)
					require.NoError(t, err)
					if lookup == "rename" {
						cfg.Evals[0].Name = "renamed"
					}
					next := reconcileCatalogPin(t, caller, ec, cfg, dir)
					if tc.oldBinding == tc.want {
						assert.Equal(t, first, next)
						assert.Len(t, service.created, 1)
					} else {
						assert.NotEqual(t, first, next)
						assert.Len(t, service.created, 2)
						after, err := json.Marshal(service.evals[first])
						require.NoError(t, err)
						assert.JSONEq(t, string(original), string(after), "rejected old histories must not be renamed")
					}
					assert.Equal(t, tc.want, service.evals[next].TestingCriteria[0].DataMapping["response"])
					assert.Equal(t, next, reconcileCatalogPin(t, caller, ec, cfg, dir))
					assert.Zero(t, service.publishes)
				})
			}
		}

	}
}

func TestExplicitResponseMappingConflictBeforePublication(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := newCatalogPinFixture(t)
			service.latest = "1"
			service.versions = map[string]json.RawMessage{"1": responseReviewContract("string")}
			cfg.Evaluators[0] = project.EvaluatorDecl{Name: "custom", Definition: map[string]any{
				"type": "rubric", "dimensions": []any{map[string]any{"id": "clarity", "weight": 5}},
			}}
			cfg.Evals[0].ID = "eval_pinned"
			cfg.Evals[0].Source = &project.SourceDecl{Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixed"}}
			service.evals["eval_pinned"] = &eval_api.OpenAIEval{
				ID: "eval_pinned", DataSourceConfig: map[string]any{"type": "azure_ai_source", "scenario": "responses"},
				TestingCriteria: []eval_api.TestingCriterion{{
					Type: "azure_ai_evaluator", Name: "custom", EvaluatorName: "custom",
					DataMapping: map[string]string{"query": "{{item.query}}", "response": "{{item.response}}"},
				}},
			}
			err := reconcileArtifactConfig(t, caller, ec, cfg, dir)
			require.ErrorContains(t, err, "stored-response mappings incompatible")
			assert.Zero(t, service.publishes)
			assert.Empty(t, service.created)
			assert.Empty(t, env.config)
		})
	}
}
