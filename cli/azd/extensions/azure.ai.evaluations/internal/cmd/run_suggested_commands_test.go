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

// A run the service returns without an eval name or id printed `--eval ` with
// nothing after it. The listing's whole claim is that the commands under it run
// as printed, and that one cannot: it is the only thing on screen that had to
// be right, and it was blank.
//
// The caller resolved an eval to fetch this run in the first place, so there is
// always an answer to fall back on.
func TestSuggestedCommandsNameTheEvalEvenWhenTheRunDoesNot(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{ID: "evalrun_1", Status: "completed"}
	items := []eval_api.OutputItem{{ID: "oi_1", Status: "completed"}}

	var out bytes.Buffer
	require.NoError(t, renderResults(&out, "the-eval", run, items, false))

	text := out.String()
	assert.NotContains(t, text, "--eval  ",
		"a suggested command with an empty --eval cannot run")
	assert.NotContains(t, text, "--eval \n")
	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, "--eval") {
			assert.Contains(t, line, "--eval the-eval", "line %q", line)
		}
	}
}

// What the run itself declares still wins: it is the name the reader
// recognizes, and the caller's identifier may be a service id.
func TestTheRunsOwnNameIsPreferredOverTheResolvedOne(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{
		ID:       "evalrun_1",
		Status:   "completed",
		Metadata: map[string]string{metaEvalName: "declared-name"},
	}

	var out bytes.Buffer
	require.NoError(t, renderResults(&out, "eval_abc123", run,
		[]eval_api.OutputItem{{ID: "oi_1", Status: "completed"}}, false))

	assert.Contains(t, out.String(), "--eval declared-name")
	assert.NotContains(t, out.String(), "eval_abc123")
}

// The export is the whole run, so it is the answer to "nothing here matched,
// where is the rest of it" -- which is exactly the case it used to be withheld
// in. It hung off the first row that survived the filter, and a --failed-only
// listing of a run that passed has no such row.
func TestExportIsOfferedEvenWhenTheFilterKeptNothing(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{
		ID:           "evalrun_1",
		Status:       "completed",
		ResultCounts: &eval_api.EvalRunResultCounts{Total: 15, Passed: 15},
	}

	var out bytes.Buffer
	require.NoError(t, renderResults(&out, "the-eval", run, nil, true))

	text := out.String()
	assert.Contains(t, text, "Export complete results:")
	assert.Contains(t, text, "--eval the-eval")
	assert.NotContains(t, text, "View details:",
		"there is no row to point at, so that one stays off")
}
