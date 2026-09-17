// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RESULTS spec 5.2: "MUST keep evaluator names, reasons, error codes,
// messages, metrics, and dimensions in `output show`, not the list."
//
// The list carried the first failure's reason in a cell truncated to
// forty-four characters, which is the worst place to read a paragraph and the
// one column whose width depended on what an evaluator happened to write.
func TestTheListLeavesReasonsToTheDetailView(t *testing.T) {
	var out bytes.Buffer
	run := &eval_api.OpenAIEvalRun{ID: "evalrun_1", Status: "completed"}
	items := []eval_api.OutputItem{{
		ID: "oi_1",
		Results: []eval_api.OutputResult{
			{Name: "coherence", Passed: new(true), Score: 5, Reason: "Read well."},
			{Name: "relevance", Passed: new(false), Score: 1, Reason: "Answered a different question."},
		},
	}}

	require.NoError(t, renderResults(&out, "an-eval", run, items, false))

	text := out.String()
	assert.NotContains(t, text, "Answered a different question.",
		"a reason is one evaluator's account of one row:\n%s", text)
	assert.NotContains(t, text, "Read well.", text)
	// What the row does say is the outcome and the arithmetic behind it.
	assert.Contains(t, text, itemFailed)
	assert.Contains(t, text, "1 passed, 1 failed")
}

// Moving it out of the list is only right if it is readable somewhere, whole.
func TestTheDetailViewCarriesEveryEvaluatorAndReason(t *testing.T) {
	var out bytes.Buffer
	item := &eval_api.OutputItem{
		ID:     "oi_1",
		RunID:  "evalrun_1",
		Status: "completed",
		Results: []eval_api.OutputResult{
			{Name: "coherence", Passed: new(true), Score: 5, Reason: "Read well."},
			{Name: "relevance", Passed: new(false), Score: 1, Reason: "Answered a different question."},
		},
	}

	require.NoError(t, renderOutputItem(&out, item))

	text := out.String()
	for _, want := range []string{
		"coherence", "Read well.",
		"relevance", "Answered a different question.",
	} {
		assert.Containsf(t, text, want,
			"`output show` is where this lives now, and it is missing %q:\n%s", want, text)
	}
}

// A passing evaluator's reason explains its score as much as a failing one's,
// so the detail view keeps both rather than only the failure.
func TestTheDetailViewKeepsAPassingReason(t *testing.T) {
	var out bytes.Buffer
	item := &eval_api.OutputItem{
		ID:     "oi_1",
		RunID:  "evalrun_1",
		Status: "completed",
		Results: []eval_api.OutputResult{
			{Name: "task_adherence", Passed: new(true), Score: 1, Reason: "Followed the task."},
		},
	}

	require.NoError(t, renderOutputItem(&out, item))

	text := out.String()
	assert.Contains(t, text, "Followed the task.", text)
	assert.Contains(t, text, "task_adherence", text)
}
