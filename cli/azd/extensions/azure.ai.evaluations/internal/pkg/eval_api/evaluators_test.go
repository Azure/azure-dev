// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The listing carries each evaluator's contract so a request can be shaped to
// match it. Reading that contract wrong means shaping the request wrong and
// taking a service-side rejection instead.
func TestEvaluatorSummaryReadsTheContract(t *testing.T) {
	var summary EvaluatorSummary
	require.NoError(t, json.Unmarshal([]byte(`{
      "name": "relevance",
      "version": "3",
      "evaluator_type": "Builtin",
      "supported_evaluation_levels": ["Run", "Turn"],
      "definition": {
        "data_schema":      {"type":"object","required":["query"],
                             "properties":{"query":{},"response":{},"context":{}}},
        "init_parameters":  {"type":"object","properties":{"model_config":{},"threshold":{}}}
      }
    }`), &summary))

	assert.Equal(t, "Builtin", summary.Type())
	assert.Equal(t, []string{"context", "query", "response"}, summary.DataSchema().PropertyNames(),
		"sorted, so a listing does not reorder itself between runs")
	assert.True(t, summary.DataSchema().Accepts("response"))
	assert.False(t, summary.DataSchema().Accepts("ground_truth"))
	assert.Equal(t, []string{"model_config", "threshold"}, summary.InitSchema().PropertyNames())
}

// The kind arrives under two names depending on the route. Reading only one
// leaves the type blank, which then reads as a custom evaluator.
func TestEvaluatorTypeAcceptsEitherSpelling(t *testing.T) {
	spelled := EvaluatorSummary{EvaluatorType: "Builtin"}
	aliased := EvaluatorSummary{TypeAlias: "Builtin"}

	assert.Equal(t, "Builtin", spelled.Type())
	assert.Equal(t, "Builtin", aliased.Type())
	assert.Empty(t, (&EvaluatorSummary{}).Type())
}

// An evaluator that declares no levels runs at any of them. Treating an empty
// list as "supports nothing" would reject every evaluator the listing does not
// describe fully.
func TestSupportsLevel(t *testing.T) {
	constrained := EvaluatorSummary{SupportedEvaluationLevels: []string{"Run", "Turn"}}
	assert.True(t, constrained.SupportsLevel("Run"))
	assert.True(t, constrained.SupportsLevel("run"), "the level is matched without regard to case")
	assert.False(t, constrained.SupportsLevel("Conversation"))
	assert.True(t, constrained.SupportsLevel(""), "asking about no level is not a constraint")

	unconstrained := EvaluatorSummary{}
	assert.True(t, unconstrained.SupportsLevel("Conversation"),
		"an evaluator that declares no levels is unconstrained, not unusable")
}

// A missing definition has to read as "not described", not crash the caller
// that asked what an evaluator accepts.
func TestEvaluatorSchemasTolerateAnAbsentDefinition(t *testing.T) {
	var absent *EvaluatorSummary
	assert.Nil(t, absent.DataSchema())
	assert.Nil(t, absent.InitSchema())

	bare := EvaluatorSummary{Name: "custom"}
	assert.Nil(t, bare.DataSchema())
	assert.Nil(t, bare.InitSchema())

	var noSchema *JSONSchema
	assert.Nil(t, noSchema.PropertyNames())
	assert.False(t, noSchema.Accepts("query"))
	assert.False(t, (&JSONSchema{}).Accepts("query"))
}

// The index is how an eval.yaml entry is matched to what the project offers.
func TestByName(t *testing.T) {
	list := &EvaluatorListResponse{Value: []EvaluatorSummary{
		{Name: "relevance", Version: "3"},
		{Name: "coherence", Version: "1"},
	}}

	index := list.ByName()
	require.Len(t, index, 2)
	assert.Equal(t, "3", index["relevance"].Version)
	assert.Nil(t, index["missing"])

	var absent *EvaluatorListResponse
	assert.Nil(t, absent.ByName())
}

// Listing built-ins is a filter on the same route, and dropping the parameter
// returns the project's custom evaluators mixed in.
func TestListEvaluatorsFiltersByType(t *testing.T) {
	client, last := recorder(t, http.StatusOK, `{"value":[{"name":"relevance"}]}`)

	_, err := client.ListEvaluators(context.Background(), EvaluatorTypeBuiltin, "v1")
	require.NoError(t, err)
	assert.Equal(t, "/evaluators", last.path)
	assert.Equal(t, "Builtin", last.query.Get("type"))

	_, err = client.ListEvaluators(context.Background(), "", "v1")
	require.NoError(t, err)
	assert.Empty(t, last.query.Get("type"), "no filter asks for everything")
}

// This route cancels only when the body is empty, and updates the run's status
// and counters when it is not. The generation-job cancel next door requires an
// empty object, so the two are easy to unify into a bug that silently rewrites
// a run instead of stopping it.
func TestCancelOpenAIEvalRunSendsNoBody(t *testing.T) {
	client, last := recorder(t, http.StatusOK, `{"id":"run_1","status":"canceled"}`)

	_, err := client.CancelOpenAIEvalRun(context.Background(), "eval_1", "run_1")

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, last.method)
	assert.Equal(t, "/openai/v1/evals/eval_1/runs/run_1", last.path)
	assert.Empty(t, last.body, "a body turns this cancel into an update")
}

// The run routes are OpenAI-compatible and carry no api-version; sending one
// is answered by a different contract than the client parses.
func TestRunRoutesAndTheirPaths(t *testing.T) {
	cases := []struct {
		name       string
		call       func(c *EvalClient) error
		wantMethod string
		wantPath   string
	}{
		{
			name:       "delete run",
			call:       func(c *EvalClient) error { return c.DeleteOpenAIEvalRun(context.Background(), "eval_1", "run_1") },
			wantMethod: http.MethodDelete,
			wantPath:   "/openai/v1/evals/eval_1/runs/run_1",
		},
		{
			name: "list output items",
			call: func(c *EvalClient) error {
				_, err := c.ListOutputItems(context.Background(), "eval_1", "run_1", 0)
				return err
			},
			wantMethod: http.MethodGet,
			wantPath:   "/openai/v1/evals/eval_1/runs/run_1/output_items",
		},
		{
			name: "get output item",
			call: func(c *EvalClient) error {
				_, err := c.GetOutputItem(context.Background(), "eval_1", "run_1", "item_1")
				return err
			},
			wantMethod: http.MethodGet,
			wantPath:   "/openai/v1/evals/eval_1/runs/run_1/output_items/item_1",
		},
		{
			name: "delete evaluator version",
			call: func(c *EvalClient) error {
				return c.DeleteEvaluatorVersion(context.Background(), "custom", "2", "v1")
			},
			wantMethod: http.MethodDelete,
			wantPath:   "/evaluators/custom/versions/2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, last := recorder(t, http.StatusOK, `{"data":[]}`)
			require.NoError(t, tc.call(client))
			assert.Equal(t, tc.wantMethod, last.method)
			assert.Equal(t, tc.wantPath, last.path)
		})
	}
}

// A limit is only sent when asked for: sending limit=0 asks the service for
// nothing rather than for everything.
func TestListOutputItemsSendsTheLimitOnlyWhenSet(t *testing.T) {
	client, last := recorder(t, http.StatusOK, `{"data":[]}`)

	_, err := client.ListOutputItems(context.Background(), "eval_1", "run_1", 50)
	require.NoError(t, err)
	assert.Equal(t, "50", last.query.Get("limit"))

	_, err = client.ListOutputItems(context.Background(), "eval_1", "run_1", 0)
	require.NoError(t, err)
	assert.Empty(t, last.query.Get("limit"), "no limit means the service's default, not zero rows")
}
