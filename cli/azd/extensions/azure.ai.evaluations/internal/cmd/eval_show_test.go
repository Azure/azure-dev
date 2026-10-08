// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `show` is documented as showing an eval definition, and answered with the id,
// the name and who created it -- true of any eval, and none of the definition.
// The graders are the part of the definition the service does return.
func TestShowSurfacesWhatTheEvalGrades(t *testing.T) {
	group := &eval_api.OpenAIEval{
		ID:   "eval_1",
		Name: "support-trace-eval",
		TestingCriteria: []eval_api.TestingCriterion{
			{Name: "task_adherence", EvaluatorName: "builtin.task_adherence"},
			{Name: "quality", EvaluatorName: "support-quality", EvaluatorVersion: "2"},
		},
	}

	assert.Equal(t, "builtin.task_adherence, support-quality (2)", evalGraders(group))
}

// The criterion label is what the service echoes when no evaluator reference
// was recorded, so it is better than printing nothing.
func TestShowFallsBackToTheCriterionLabel(t *testing.T) {
	group := &eval_api.OpenAIEval{
		TestingCriteria: []eval_api.TestingCriterion{{Name: "custom-grader"}},
	}

	assert.Equal(t, "custom-grader", evalGraders(group))
}

// An older eval, or one the service answers without a definition, still shows
// its identity rather than blank rows.
func TestShowOmitsWhatTheServiceDidNotSend(t *testing.T) {
	assert.Empty(t, evalGraders(&eval_api.OpenAIEval{ID: "eval_1"}))
	assert.Empty(t, evalGraders(nil))

	// A criterion carrying no name at all contributes nothing rather than an
	// empty entry with a stray separator.
	assert.Empty(t, evalGraders(&eval_api.OpenAIEval{
		TestingCriteria: []eval_api.TestingCriterion{{Type: "azure_ai_evaluator"}},
	}))
}

func TestEvalShowDisplaysNonCustomSourceDefinition(t *testing.T) {
	for _, tc := range []struct {
		name     string
		config   map[string]any
		source   string
		scenario string
	}{
		{
			name: "responses", config: map[string]any{"type": "azure_ai_source", "scenario": "responses"},
			source: "azure_ai_source", scenario: "responses",
		},
		{
			name: "traces", config: map[string]any{"type": "azure_ai_source", "scenario": "traces_preview"},
			source: "azure_ai_source", scenario: "traces_preview",
		},
		{name: "custom", config: map[string]any{"type": "custom", "include_sample_schema": false}},
		{name: "legacy projection"},
		{name: "empty projection", config: map[string]any{}},
		{name: "source only", config: map[string]any{"type": "azure_ai_source"}, source: "azure_ai_source"},
		{name: "unrecognized source", config: map[string]any{"type": "future_source"}, source: "future_source"},
		{name: "non-string type", config: map[string]any{"type": 42, "scenario": "responses"}},
		{
			name: "non-string scenario", config: map[string]any{"type": "azure_ai_source", "scenario": []any{"responses"}},
			source: "azure_ai_source",
		},
	} {
		for _, format := range []string{"", "json"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				group := &eval_api.OpenAIEval{
					ID: "eval_fixed", Name: "quality", CreatedAt: 1700000000, CreatedBy: "creator",
					DataSourceConfig: tc.config,
					TestingCriteria: []eval_api.TestingCriterion{{
						EvaluatorName: "builtin.coherence", EvaluatorVersion: "1",
					}},
				}
				body, err := json.Marshal(group)
				require.NoError(t, err)
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					assert.Equal(t, http.MethodGet, r.Method)
					assert.Equal(t, "/openai/v1/evals/eval_fixed", r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					_, err := w.Write(body)
					assert.NoError(t, err)
				}))
				t.Cleanup(server.Close)
				var out bytes.Buffer
				cmd := jsonCmd(t, format)
				cmd.SetContext(t.Context())
				cmd.SetOut(&out)
				action := &evalShowAction{
					cmd: cmd, evalID: group.ID,
					newContext: func(context.Context, string) (*evalContext, error) {
						return evalContextFor(server), nil
					},
				}
				require.NoError(t, action.Run())
				assert.Equal(t, 1, requests, "show must remain a single read without invoking an agent")
				if format == "json" {
					assert.JSONEq(t, string(body), out.String(), "the machine-readable definition must remain unchanged")
					decoder := json.NewDecoder(&out)
					require.NoError(t, decoder.Decode(new(any)))
					assert.Equal(t, io.EOF, decoder.Decode(new(any)))
					return
				}
				text := out.String()
				assert.Contains(t, text, "eval_fixed")
				assert.Contains(t, text, "quality")
				assert.Contains(t, text, "creator")
				assert.Contains(t, text, "builtin.coherence (1)")
				rows := map[string]string{}
				for line := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
					key, value, found := strings.Cut(line, "   ")
					require.True(t, found, "expected a key/value detail row: %q", line)
					rows[strings.TrimSpace(key)] = strings.TrimSpace(value)
				}
				if tc.source == "" {
					assert.NotContains(t, rows, "Data Source")
				} else {
					assert.Equal(t, tc.source, rows["Data Source"])
				}
				if tc.scenario == "" {
					assert.NotContains(t, rows, "Scenario")
				} else {
					assert.Equal(t, tc.scenario, rows["Scenario"])
				}
			})
		}
	}
}
