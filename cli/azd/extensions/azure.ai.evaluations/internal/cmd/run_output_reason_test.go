// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A row where everything passed still says what it found.
//
// The reason was only captured for evaluators that failed, so a run where
// every sample passed printed an empty REASON column and a dash under
// EVALUATORS -- which reads as a listing that failed to render rather than a
// run that went well.
func TestAPassingRowStillCarriesAReason(t *testing.T) {
	var out bytes.Buffer
	run := &eval_api.OpenAIEvalRun{ID: "evalrun_1", Status: "completed"}
	items := []eval_api.OutputItem{{
		ID: "oi_1",
		Results: []eval_api.OutputResult{
			{Name: "task_adherence", Passed: new(true), Score: 1, Reason: "Followed the task."},
		},
	}}

	require.NoError(t, renderResults(&out, run, items, false))

	text := out.String()
	assert.Contains(t, text, "Followed the task.",
		"a passing evaluator's reason is what explains the score:\n%s", text)
	assert.Contains(t, text, "all passed",
		"the evaluators column says what it found rather than printing a dash")
	assert.NotContains(t, text, "\t-\t")
}

// On a mixed row the failure is what the reader came for, so its reason wins
// even when a passing evaluator was returned first.
func TestAFailureReasonOutranksAPassingOne(t *testing.T) {
	var out bytes.Buffer
	run := &eval_api.OpenAIEvalRun{ID: "evalrun_1", Status: "completed"}
	items := []eval_api.OutputItem{{
		ID: "oi_1",
		Results: []eval_api.OutputResult{
			{Name: "coherence", Passed: new(true), Score: 5, Reason: "Read well."},
			{Name: "relevance", Passed: new(false), Score: 1, Reason: "Answered a different question."},
		},
	}}

	require.NoError(t, renderResults(&out, run, items, false))

	text := out.String()
	assert.Contains(t, text, "Answered a different question.")
	assert.NotContains(t, text, "Read well.",
		"the passing reason must not stand in front of the failure:\n%s", text)
	assert.Contains(t, text, "relevance")
	assert.False(t, strings.Contains(text, "all passed"),
		"a row with a failure has not all passed")
}
