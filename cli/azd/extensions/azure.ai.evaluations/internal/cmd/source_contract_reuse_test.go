// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

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
