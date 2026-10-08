// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A shared project runs to hundreds of evals, so reading your own out of the
// listing is the task. The filter is applied before either view renders, so
// `-o json` and the table cannot answer differently.
func TestEvalListFiltersByName(t *testing.T) {
	evals := []eval_api.OpenAIEval{
		{ID: "eval_1", Name: "shruti-trace-eval"},
		{ID: "eval_2", Name: "mohamed-regression"},
		{ID: "eval_3", Name: "SHRUTI-ds-eval"},
	}

	kept := filterEvalsByName(evals, "shruti")

	require.Len(t, kept, 2, "the comparison ignores case")
	assert.Equal(t, "eval_1", kept[0].ID)
	assert.Equal(t, "eval_3", kept[1].ID)
}

// An absent filter keeps everything, so the caller has one path.
func TestEvalListWithoutAFilterKeepsEverything(t *testing.T) {
	evals := []eval_api.OpenAIEval{{ID: "eval_1", Name: "a"}, {ID: "eval_2", Name: "b"}}
	assert.Len(t, filterEvalsByName(evals, ""), 2)
}

// A filter that matches nothing says how many there were, so the reader can
// tell an empty project from a filter that was too narrow.
func TestAnEmptyFilterResultSaysWhatWasThere(t *testing.T) {
	line := messages.NoEvalsMatching("nobody", 384)
	assert.Contains(t, line, "nobody")
	assert.Contains(t, line, "384")
	assert.Contains(t, line, "--name")
}

// Cobra's own "accepts 1 arg(s), received 0" names neither the argument nor
// where to find one.
func TestOutputShowSaysWhatItNeeds(t *testing.T) {
	err := messages.OutputItemRequired("shruti-trace-eval")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "run output list", "it names the command that lists them")
	assert.Contains(t, err.Error(), "ITEM", "the column the value comes from")
	assert.Contains(t, err.Error(), "shruti-trace-eval", "it keeps the eval already given")
	assert.NotContains(t, err.Error(), "accepts 1 arg")
}

// The wait is the default because a gate needs the verdict, so the way to
// detach has to be visible rather than left in --help.
func TestWaitingSaysHowToDetach(t *testing.T) {
	line := messages.WaitingForRun("evalrun_1")
	assert.Contains(t, line, "evalrun_1")
	assert.Contains(t, line, "--no-wait")
	assert.True(t, strings.Contains(line, "Ctrl-C"), "leaving it running is the other way out")
}

// The fallback line is where a reader learns the run was chosen for them, so
// it is also where they learn how to choose.
func TestTheFallbackLineSaysAnotherRunCanBeNamed(t *testing.T) {
	line := messages.UsingLastRun("evalrun_1")
	assert.Contains(t, line, "evalrun_1")
	assert.Contains(t, line, "--run", "it names the flag, and every run command accepts it")
}
