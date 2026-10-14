// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureEvalSourceContractTransitions(t *testing.T) {
	for _, before := range []string{project.SourceTypeTraces, project.SourceTypeResponses} {
		for _, legacy := range []bool{false, true} {
			for _, rename := range []bool{false, true} {
				name := before
				if legacy {
					name += "/legacy-v2"
				}
				if rename {
					name += "/rename"
				}
				t.Run(name, func(t *testing.T) {
					group := sourceContractGroup(before)
					after := project.SourceTypeResponses
					if before == after {
						after = project.SourceTypeTraces
					}
					changed := sourceContractGroup(after)
					if rename {
						changed.Name = "renamed"
					}
					r, env, posts, updates := sourceContractReconciler(t, group, legacy, rename, &changed)
					id, created, err := r.EnsureEval(t.Context(), changed, "")
					require.NoError(t, err)
					require.True(t, created)
					require.Equal(t, "eval_new", id)
					require.Len(t, *posts, 1)
					require.Zero(t, *updates, "do not rename an incompatible old eval before replacing it")
					want, err := buildEvalRequest(&changed, r.ec.schemas, nil)
					require.NoError(t, err)
					require.Equal(t, want.TestingCriteria, (*posts)[0].TestingCriteria)
					require.Equal(t, want.DataSourceConfig.IncludeSampleSchema,
						(*posts)[0].DataSourceConfig.IncludeSampleSchema)
					require.Equal(t, "eval_new", env.stored(t, idKey("eval", changed.Name)))
				})
			}
		}
	}
}

func TestEnsureEvalSourceContractIgnoresRunOnlyChanges(t *testing.T) {
	for _, mode := range []string{project.SourceTypeTraces, project.SourceTypeResponses} {
		for _, legacy := range []bool{false, true} {
			t.Run(mode, func(t *testing.T) {
				group := sourceContractGroup(mode)
				changed := sourceContractGroup(mode)
				if mode == project.SourceTypeTraces {
					changed.Source.AgentName = "another-agent"
					changed.Source.LookbackHours = 48
					changed.Source.MaxTraces = 10
				} else {
					changed.Source.ResponseIDs = []string{"resp_second"}
					changed.Source.MaxTurns = 3
				}
				r, env, posts, _ := sourceContractReconciler(t, group, legacy, false, &changed)
				id, created, err := r.EnsureEval(t.Context(), changed, "")
				require.NoError(t, err)
				require.False(t, created)
				require.Equal(t, "eval_old", id)
				require.Empty(t, *posts, "no upgrade or run-filter change may duplicate a compatible eval")
				definition, err := project.FingerprintDefinition(changed)
				require.NoError(t, err)
				require.Equal(t, fingerprintEra+definition,
					env.stored(t, project.FingerprintKey("eval", changed.Name)))
			})
		}
	}
}

func TestEnsureEvalOldDefaultsRequireDeliberateCriterionChange(t *testing.T) {
	for _, migrate := range []bool{false, true} {
		name := "unchanged upgrade"
		if migrate {
			name = "deliberate migration"
		}
		t.Run(name, func(t *testing.T) {
			previous := sourceContractGroup(project.SourceTypeTraces)
			desired := sourceContractGroup(project.SourceTypeTraces)
			if migrate {
				desired.Evaluators[0].Name = "coherence_mapped"
			}
			r, _, posts, _ := sourceContractReconciler(t, previous, true, false, &desired,
				func(stored *eval_api.CreateOpenAIEvalRequest) {
					stored.TestingCriteria[0].DataMapping["context"] = "{{item.context}}"
					delete(stored.TestingCriteria[0].DataMapping, "tool_calls")
				})
			id, created, err := r.EnsureEval(t.Context(), desired, "")
			require.NoError(t, err)
			require.Equal(t, migrate, created)
			if !migrate {
				require.Equal(t, "eval_old", id)
				require.Empty(t, *posts, "do not recreate old evals merely because defaults changed")
				return
			}
			require.Equal(t, "eval_new", id)
			require.Len(t, *posts, 1)
			require.NotContains(t, (*posts)[0].TestingCriteria[0].DataMapping, "context")
			require.Equal(t, "{{item.tool_calls}}", (*posts)[0].TestingCriteria[0].DataMapping["tool_calls"])
			require.Equal(t, "coherence_mapped", (*posts)[0].TestingCriteria[0].Name)
		})
	}
}

func TestExplicitSourceContractConflictBeforePublication(t *testing.T) {
	for _, caller := range []string{"create", "up", "ensure"} {
		for _, tc := range []struct {
			name, source, field, stored, authored string
			sampled                               bool
			criterion                             string
			builtin                               bool
		}{
			{name: "trace sample schema", source: project.SourceTypeTraces, sampled: true},
			{
				name: "trace sample binding", source: project.SourceTypeTraces,
				field: "response", stored: "{{sample.output_text}}",
			},
			{
				name: "trace authored item field", source: project.SourceTypeTraces,
				field: "query", stored: "{{item.old}}", authored: "{{item.new}}",
			},
			{
				name: "response authored item field", source: project.SourceTypeResponses,
				field: "query", stored: "{{item.old}}", authored: "{{item.new}}",
			},
			{
				name: "response authored sample field", source: project.SourceTypeResponses,
				field: "response", stored: "{{sample.old}}", authored: "{{sample.new}}",
			},
			{
				name: "authored nonstandard field", source: project.SourceTypeResponses,
				field: "context", stored: "{{item.old}}", authored: "{{item.new}}",
			},
			{
				name: "missing authored binding", source: project.SourceTypeResponses,
				field: "query", authored: "{{item.new}}",
			},
			{
				name: "authored literal", source: project.SourceTypeResponses,
				field: "context", stored: "old", authored: "new",
			},
			{
				name: "missing trace criterion", source: project.SourceTypeTraces,
				field: "query", stored: "{{item.new}}", authored: "{{item.new}}", criterion: "missing",
			},
			{
				name: "renamed trace criterion", source: project.SourceTypeTraces,
				field: "query", stored: "{{item.new}}", authored: "{{item.new}}", criterion: "renamed",
			},
			{
				name: "different trace evaluator", source: project.SourceTypeTraces,
				field: "query", stored: "{{item.new}}", authored: "{{item.new}}", criterion: "different evaluator",
			},
			{
				name: "missing response criterion", source: project.SourceTypeResponses,
				field: "query", stored: "{{item.new}}", authored: "{{item.new}}", criterion: "missing",
			},
			{
				name: "renamed response criterion", source: project.SourceTypeResponses,
				field: "query", stored: "{{item.new}}", authored: "{{item.new}}", criterion: "renamed",
			},
			{
				name: "different response evaluator", source: project.SourceTypeResponses,
				field: "query", stored: "{{item.new}}", authored: "{{item.new}}", criterion: "different evaluator",
			},
			{
				name: "builtin trace mapping", source: project.SourceTypeTraces, builtin: true,
				field: "query", stored: "{{item.old}}", authored: "{{item.new}}",
			},
			{
				name: "builtin response mapping", source: project.SourceTypeResponses, builtin: true,
				field: "query", stored: "{{item.old}}", authored: "{{item.new}}",
			},
			{
				name: "missing builtin trace criterion", source: project.SourceTypeTraces, builtin: true,
				field: "query", stored: "{{item.new}}", authored: "{{item.new}}", criterion: "missing",
			},
			{
				name: "renamed builtin response criterion", source: project.SourceTypeResponses, builtin: true,
				field: "query", stored: "{{item.new}}", authored: "{{item.new}}", criterion: "renamed",
			},
		} {
			t.Run(caller+"/"+tc.name, func(t *testing.T) {
				ec, env, service, cfg, dir := newCatalogPinFixture(t)
				service.latest = "1"
				service.versions = map[string]json.RawMessage{"1": responseReviewContract("string")}
				cfg.Evaluators[0] = project.EvaluatorDecl{Name: "custom", Definition: map[string]any{
					"type": "rubric", "dimensions": []any{map[string]any{"id": "clarity", "weight": 5}},
				}}
				group := sourceContractGroup(tc.source)
				group.ID = "eval_pinned"
				group.Evaluators = evalcore.EvaluatorList{{Evaluator: "custom", Name: "named_criterion"}}
				if tc.builtin {
					group.Evaluators[0].Evaluator = "builtin.coherence"
				}
				if tc.authored != "" {
					group.Evaluators[0].DataMapping = map[string]string{tc.field: tc.authored}
				}
				cfg.Evals = []project.Eval{group}
				contract, err := evaluatorContract(service.versions["1"])
				require.NoError(t, err)
				request, err := buildEvalRequest(&group,
					map[string]*eval_api.EvaluatorSummary{group.Evaluators[0].Evaluator: contract}, nil)
				require.NoError(t, err)
				configJSON, err := json.Marshal(request.DataSourceConfig)
				require.NoError(t, err)
				remote := &eval_api.OpenAIEval{ID: group.ID, Name: "existing", TestingCriteria: request.TestingCriteria}
				require.NoError(t, json.Unmarshal(configJSON, &remote.DataSourceConfig))
				service.evals[group.ID] = remote
				r := &evalReconciler{ec: ec}
				if caller == "ensure" {
					require.NoError(t, r.Validate(t.Context(), cfg, dir))
				}
				if tc.sampled {
					remote.DataSourceConfig["include_sample_schema"] = true
				}
				if tc.field != "" {
					if tc.stored == "" {
						delete(remote.TestingCriteria[0].DataMapping, tc.field)
					} else {
						remote.TestingCriteria[0].DataMapping[tc.field] = tc.stored
					}
				}
				switch tc.criterion {
				case "missing":
					remote.TestingCriteria = nil
				case "renamed":
					remote.TestingCriteria[0].Name = "another_criterion"
				case "different evaluator":
					remote.TestingCriteria[0].EvaluatorName = "another_evaluator"
				}
				before, err := json.Marshal(remote)
				require.NoError(t, err)
				if caller == "ensure" {
					id, created, ensureErr := r.EnsureEval(t.Context(), group, "")
					assert.Empty(t, id)
					assert.False(t, created)
					err = ensureErr
				} else {
					err = reconcileArtifactConfig(t, caller, ec, cfg, dir)
				}
				require.ErrorContains(t, err, "incompatible with the declared source")
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
				assert.Contains(t, local.Suggestion, "explicit id")
				assert.Zero(t, service.publishes)
				assert.Empty(t, service.created)
				assert.Empty(t, env.config)
				assert.Empty(t, env.values)
				after, err := json.Marshal(remote)
				require.NoError(t, err)
				assert.JSONEq(t, string(before), string(after), "rejection must preserve the immutable eval")
			})
		}
	}
}

func TestSourceContractPreservesInferredDefaultsAndMatchingAuthoredMappings(t *testing.T) {
	for _, mode := range []string{project.SourceTypeTraces, project.SourceTypeResponses} {
		for _, tc := range []struct {
			name, stored, authored string
			missingCriterion       bool
		}{
			{name: "missing inferred mapping"},
			{name: "missing inferred criterion", missingCriterion: true},
			{name: "changed inferred item field", stored: "{{item.old}}"},
			{name: "matching authored mapping", stored: "{{item.new}}", authored: "{{item.new}}"},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				group := sourceContractGroup(mode)
				group.Evaluators[0].Name = "named_criterion"
				if tc.authored != "" {
					group.Evaluators[0].DataMapping = map[string]string{"query": tc.authored}
				}
				request, err := buildEvalRequest(&group, nil, nil)
				require.NoError(t, err)
				remote := &eval_api.OpenAIEval{TestingCriteria: []eval_api.TestingCriterion{{
					Name: "named_criterion", EvaluatorName: group.Evaluators[0].Evaluator,
					DataMapping: map[string]string{"service_added": "{{item.enrichment}}"},
				}}}
				if tc.stored != "" {
					remote.TestingCriteria[0].DataMapping["query"] = tc.stored
				}
				if tc.missingCriterion {
					remote.TestingCriteria = nil
				}
				assert.False(t, conflictingSourceContract(group, remote, request))
			})
		}
	}
}

func TestManagedSourceContractReplacesMissingAuthoredCriterion(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, source := range []string{project.SourceTypeTraces, project.SourceTypeResponses} {
			for _, lookup := range []string{"cached", "rename"} {
				t.Run(caller+"/"+source+"/"+lookup, func(t *testing.T) {
					ec, _, service, cfg, dir := newCatalogPinFixture(t)
					service.versions = map[string]json.RawMessage{"1": responseReviewContract("string")}
					cfg.Evals[0].Source = sourceContractGroup(source).Source
					cfg.Evals[0].Evaluators[0].Name = "authored_criterion"
					cfg.Evals[0].Evaluators[0].DataMapping = map[string]string{"query": "{{item.custom_query}}"}
					first := reconcileCatalogPin(t, caller, ec, cfg, dir)
					service.evals[first].TestingCriteria = nil
					before, err := json.Marshal(service.evals[first])
					require.NoError(t, err)
					if lookup == "rename" {
						cfg.Evals[0].Name = "renamed"
					}
					next := reconcileCatalogPin(t, caller, ec, cfg, dir)
					require.NotEqual(t, first, next)
					require.Len(t, service.created, 2)
					after, err := json.Marshal(service.evals[first])
					require.NoError(t, err)
					assert.JSONEq(t, string(before), string(after), "leave the old eval and its history untouched")
					require.Len(t, service.evals[next].TestingCriteria, 1)
					assert.Equal(t, "authored_criterion", service.evals[next].TestingCriteria[0].Name)
					assert.Equal(t, "{{item.custom_query}}", service.evals[next].TestingCriteria[0].DataMapping["query"])
					assert.Equal(t, next, reconcileCatalogPin(t, caller, ec, cfg, dir))
					assert.Len(t, service.created, 2, "retry must not create another eval")
					assert.Zero(t, service.publishes)
				})
			}
		}
	}
}

func TestManagedBuiltinSourceContractHonorsAuthoredMappings(t *testing.T) {
	for _, mode := range []string{project.SourceTypeTraces, project.SourceTypeResponses} {
		for _, rename := range []bool{false, true} {
			for _, storedMapping := range []string{"{{item.old}}", "{{item.new}}"} {
				t.Run(mode+"/"+storedMapping, func(t *testing.T) {
					group := sourceContractGroup(mode)
					group.Evaluators[0].DataMapping = map[string]string{"query": "{{item.new}}"}
					desired := group
					if rename {
						desired.Name = "renamed"
					}
					r, _, posts, updates := sourceContractReconciler(t, group, false, rename, &desired,
						func(stored *eval_api.CreateOpenAIEvalRequest) {
							stored.TestingCriteria[0].DataMapping["query"] = storedMapping
						})
					id, created, err := r.EnsureEval(t.Context(), desired, "")
					require.NoError(t, err)
					if storedMapping == "{{item.new}}" {
						assert.Equal(t, "eval_old", id)
						assert.False(t, created)
						assert.Empty(t, *posts)
					} else {
						assert.Equal(t, "eval_new", id)
						assert.True(t, created)
						require.Len(t, *posts, 1)
						assert.Equal(t, "builtin.coherence", (*posts)[0].TestingCriteria[0].EvaluatorName)
						assert.Equal(t, "{{item.new}}", (*posts)[0].TestingCriteria[0].DataMapping["query"])
						assert.Zero(t, *updates, "do not rename the incompatible immutable eval")
					}
				})
			}
		}
	}
}

func TestExplicitSourceContractKeepsMatchingAuthoredMappings(t *testing.T) {
	for _, mode := range []string{project.SourceTypeTraces, project.SourceTypeResponses} {
		for _, evaluator := range []string{"custom", "builtin.coherence"} {
			t.Run(mode+"/"+evaluator, func(t *testing.T) {
				group := sourceContractGroup(mode)
				group.ID = "eval_old"
				group.Evaluators[0].Evaluator = evaluator
				group.Evaluators[0].DataMapping = map[string]string{"query": "{{item.custom_query}}"}
				r, env, posts, updates := sourceContractReconciler(t, group, false, false, &group)
				request, err := buildEvalRequest(&group, r.ec.schemas, nil)
				require.NoError(t, err)
				r.prepared = map[string]preparedEval{group.Name: {group: group, request: request}}
				id, created, err := r.EnsureEval(t.Context(), group, "")
				require.NoError(t, err)
				assert.Equal(t, group.ID, id)
				assert.False(t, created)
				assert.Empty(t, *posts)
				assert.Zero(t, *updates)
				assert.Empty(t, env.config)
				assert.Empty(t, env.values)
			})
		}
	}
}

func sourceContractGroup(mode string) project.Eval {
	group := project.Eval{
		Name: "quality", EvaluationLevel: "turn",
		Evaluators: []evalcore.EvaluatorRef{{Evaluator: "builtin.coherence"}},
		Source:     &project.SourceDecl{Type: mode},
	}
	if mode == project.SourceTypeTraces {
		group.Source.AgentName = "recorded-agent"
		group.Source.LookbackHours = 24
	} else {
		group.Source.ResponseIDs = []string{"resp_first"}
	}
	return group
}

func sourceContractReconciler(
	t *testing.T, previous project.Eval, legacy, rename bool, desired *project.Eval,
	configure ...func(*eval_api.CreateOpenAIEvalRequest),
) (*evalReconciler, *testEnvServer, *[]eval_api.CreateOpenAIEvalRequest, *int) {
	t.Helper()
	contract := schema("builtin.coherence", nil, []string{"query", "response", "messages", "tool_calls"},
		nil, nil, "turn", "conversation")
	contract.Definition.DataSchema.Properties["response"] = map[string]any{"type": "string"}
	schemas := map[string]*eval_api.EvaluatorSummary{contract.Name: contract}
	stored, err := buildEvalRequest(&previous, schemas, nil)
	require.NoError(t, err)
	for _, configureStored := range configure {
		configureStored(stored)
	}
	baseline := previous
	if legacy {
		baseline.Source = nil // v2 previously erased the complete source.
	}
	fingerprint, err := project.FingerprintDefinition(baseline)
	require.NoError(t, err)
	env := &testEnvServer{state: map[string]string{
		project.FingerprintKey("eval", previous.Name): "v2:" + fingerprint,
		idKey("eval", previous.Name):                  "eval_old",
	}}
	if rename {
		// Before source modes were hashed, a stale identity index could point
		// at the opposite source contract. Do not mutate it while adopting.
		digest, err := project.FingerprintGroup(*desired)
		require.NoError(t, err)
		env.state[digestIDKey(digest)] = "eval_old"
	}
	var posts []eval_api.CreateOpenAIEvalRequest
	updates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/evals"):
			var request eval_api.CreateOpenAIEvalRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			posts = append(posts, request)
			_, _ = w.Write([]byte(`{"id":"eval_new"}`))
		case r.Method == http.MethodPost:
			updates++
			_, _ = w.Write([]byte(`{"id":"eval_old"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/eval_old"):
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id": "eval_old", "name": previous.Name, "metadata": stored.Metadata,
				"testing_criteria": stored.TestingCriteria, "data_source_config": stored.DataSourceConfig,
			}))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	ec := evalContextFor(srv)
	ec.schemas = schemas
	ec.azdClient, ec.envName = newTestAzdClient(t, env), "test"
	return &evalReconciler{ec: ec}, env, &posts, &updates
}
