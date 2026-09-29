// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The detail view leads with the outcome, not the lifecycle status.
//
// The service reports a row whose every result errored as `completed`, so
// leading with that made a failed test case read as a success. Both are shown,
// because dropping the service's own says nothing about which of the two the
// reader is looking at.
func TestTheDetailViewLeadsWithTheOutcome(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderOutputItem(&out, &eval_api.OutputItem{
		ID:     "oi_3",
		RunID:  "evalrun_1",
		Status: "completed",
		Results: []eval_api.OutputResult{
			{Name: "task_adherence", Passed: new(false), Score: 0, Reason: "Answered a different question."},
		},
	}))

	text := out.String()
	assert.Contains(t, text, "TEST CASE — FAILED")
	assert.Contains(t, text, "oi_3")
	assert.Contains(t, text, "evalrun_1")
	assert.Contains(t, text, "1 failed", "the counts say how many results stand behind it")
	assert.Contains(t, text, "completed",
		"and the lifecycle status is kept, so the two are distinguishable")
}

// This command is the one place a reason is not truncated, which is the reason
// to run it: the listing above already showed the clipped version.
func TestTheDetailViewPrintsReasonsWhole(t *testing.T) {
	long := strings.Repeat("the judge explained itself at length. ", 12)

	var out bytes.Buffer
	require.NoError(t, renderOutputItem(&out, &eval_api.OutputItem{
		ID: "oi_1",
		Results: []eval_api.OutputResult{
			{Name: "relevance", Passed: new(false), Reason: long},
		},
	}))

	assert.Contains(t, out.String(), strings.TrimSpace(long))
	assert.NotContains(t, out.String(), "...", "nothing here is clipped")
}

// An evaluator that failed to run recorded an error, not a judgement, and the
// code it carries is what the reader would otherwise go to the portal for.
func TestTheDetailViewReportsAnErrorAsAnError(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderOutputItem(&out, &eval_api.OutputItem{
		ID: "oi_2",
		Results: []eval_api.OutputResult{{
			Name: "Relevance",
			Sample: &eval_api.OutputSample{Error: &eval_api.SampleError{
				Code: "FAILED_EXECUTION", Message: "Response string cannot be empty.",
			}},
		}},
	}))

	text := out.String()
	assert.Contains(t, text, "TEST CASE — ERRORED")
	assert.Contains(t, text, "EVALUATOR: Relevance")
	assert.Contains(t, text, "FAILED_EXECUTION")
	assert.Contains(t, text, "Error:", "an evaluator that never ran did not give a reason")
	assert.Contains(t, text, "Response string cannot be empty.")
}

// A skip is the service declining to grade, and it says why.
func TestTheDetailViewReportsASkipAsASkip(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderOutputItem(&out, &eval_api.OutputItem{
		ID: "oi_1",
		Results: []eval_api.OutputResult{{
			Name: "customer_satisfaction", Status: "skipped",
			Reason: "The conversation is incomplete and cannot be evaluated.",
		}},
	}))

	text := out.String()
	assert.Contains(t, text, "TEST CASE — SKIPPED")
	assert.Contains(t, text, "1 skipped")
	assert.Contains(t, text, "The conversation is incomplete")
	assert.NotContains(t, text, "Error:", "a skip is not a failure to run")
}

// A rubric's dimensions are the substance of it, so they get their own table.
func TestTheDetailViewTablesRubricDimensions(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderOutputItem(&out, &eval_api.OutputItem{
		ID: "oi_1",
		Results: []eval_api.OutputResult{
			{Name: "custom_rubric", Metric: "correct_interpretation", Score: 0.4,
				Passed: new(false), Reason: "Missed the ask."},
			{Name: "custom_rubric", Metric: "actionable_insights", Score: 0.7,
				Passed: new(true), Reason: "Gave next steps."},
		},
	}))

	text := out.String()
	assert.Contains(t, text, "RUBRIC DIMENSIONS")
	assert.Contains(t, text, "correct_interpretation")
	assert.Contains(t, text, "actionable_insights")
	assert.Contains(t, text, "Missed the ask.")
}

// A reader who cannot see dimensions needs to know whether the rubric has none
// or the response did not carry them. Inventing rows answers a question about
// the run with something made up here.
func TestMissingRubricDimensionsAreReportedNotInvented(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, renderOutputItem(&out, &eval_api.OutputItem{
		ID: "oi_1",
		Results: []eval_api.OutputResult{
			{Name: "custom_rubric", Score: 0.58, Passed: new(true)},
		},
	}))

	assert.Contains(t, out.String(), "not returned by service")

	// And a built-in has no dimensions to be missing, so it says nothing.
	var builtin bytes.Buffer
	require.NoError(t, renderOutputItem(&builtin, &eval_api.OutputItem{
		ID:      "oi_1",
		Results: []eval_api.OutputResult{{Name: "coherence", Passed: new(true), Score: 4}},
	}))
	assert.NotContains(t, builtin.String(), "not returned by service")
}

// The export holds every evaluated row, so it carries prompts, answers and
// reasons. A failure must leave the previous file intact rather than a
// truncated one that still parses.
func TestAnAtomicWriteLeavesTheOldFileWhenItFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"run":"previous"}`), 0o600))

	boom := errors.New("the service stopped sending")
	err := writeFileAtomicFunc(path, func(w io.Writer) error {
		_, _ = w.Write([]byte(`{"run":"partial`))
		return boom
	})
	require.Error(t, err)

	body, readErr := os.ReadFile(path) //nolint:gosec // this test's own temp dir
	require.NoError(t, readErr)
	assert.Equal(t, `{"run":"previous"}`, string(body),
		"a half-written export must not replace the one that was there")

	entries, readErr := os.ReadDir(filepath.Dir(path))
	require.NoError(t, readErr)
	assert.Len(t, entries, 1, "and the temporary file must not be left behind")
}

// The ordinary path still writes what it was given.
func TestAnAtomicWriteReplacesOnSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.json")

	require.NoError(t, writeFileAtomicFunc(path, func(w io.Writer) error {
		_, err := w.Write([]byte(`{"run":"new"}`))
		return err
	}))

	body, err := os.ReadFile(path) //nolint:gosec // this test's own temp dir
	require.NoError(t, err)
	assert.Equal(t, `{"run":"new"}`, string(body))
}
