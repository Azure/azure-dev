// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The flag is the answer, spelled however it was typed.
func TestResolveEvaluationLevel_FlagWins(t *testing.T) {
	for _, given := range []string{"turn", "TURN", "  Conversation "} {
		got, err := resolveEvaluationLevel(noPromptCmd(t, false), given)
		require.NoError(t, err, given)
		assert.Contains(t, evaluationLevels, got,
			"a level the file accepts, not the casing the terminal sent")
	}

	got, err := resolveEvaluationLevel(noPromptCmd(t, true), "conversation")
	require.NoError(t, err)
	assert.Equal(t, project.EvaluationLevelConversation, got)
}

// An unknown level fails before anything is written, not when the service
// rejects the file two commands later.
func TestResolveEvaluationLevel_UnknownIsRefused(t *testing.T) {
	_, err := resolveEvaluationLevel(noPromptCmd(t, true), "sentence")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "turn")
	assert.Contains(t, err.Error(), "conversation",
		"a refusal has to name what it would have accepted")
}

// With nobody to ask, the documented default applies rather than a refusal:
// the level has an answer that is right for most evals, which is what makes it
// a default rather than a required input.
func TestResolveEvaluationLevel_DefaultsUnderNoPrompt(t *testing.T) {
	got, err := resolveEvaluationLevel(noPromptCmd(t, true), "")
	require.NoError(t, err)
	assert.Equal(t, project.EvaluationLevelTurn, got)
}

// The window is written in hours because that is what the file records, and
// the flag is in days because that is how the question reads.
func TestResolveTraceWindow_FlagWins(t *testing.T) {
	for days, wantHours := range map[int]int{1: 24, 7: 168, 30: 720} {
		got, err := resolveTraceWindow(noPromptCmd(t, false), days, true)
		require.NoError(t, err, days)
		assert.Equal(t, wantHours, got)
	}
}

// The flag accepts exactly what the prompt offers.
//
// A flag that took any number would let --no-prompt write configurations the
// wizard could not, and the two would drift. The escape hatch is the file, and
// the refusal has to say so or it reads as a limitation rather than a routing.
func TestResolveTraceWindow_OffPresetIsRefused(t *testing.T) {
	for _, days := range []int{0, -1, 3, 14, 365} {
		_, err := resolveTraceWindow(noPromptCmd(t, true), days, true)
		require.Error(t, err, days)
		assert.Contains(t, err.Error(), "lookback_hours",
			"the refusal has to name where any other window is set")
	}
}

// Untouched, the week the spec writes as lookback_hours: 168.
func TestResolveTraceWindow_DefaultsToAWeek(t *testing.T) {
	got, err := resolveTraceWindow(noPromptCmd(t, true), defaultTraceWindowDays, false)
	require.NoError(t, err)
	assert.Equal(t, 168, got)

	// The flag's own default must not be read as a request, or every
	// dataset-backed init would be refused for passing a trace flag.
	got, err = resolveTraceWindow(noPromptCmd(t, true), 1, false)
	require.NoError(t, err)
	assert.Equal(t, 168, got, "an untouched flag is not an answer, whatever it holds")
}

// The scaffold has to record the window and the level, because they are what
// decides which rows the eval read and what it called a sample. Left off, both
// were whatever the service chose on the day it ran.
func TestScaffold_WritesLevelAndWindow(t *testing.T) {
	plan, _ := scaffoldFor(t, scaffoldInput{
		evalName: "smoke", target: "a", source: initSourceTraces,
		maxTraces: 20, lookbackHours: 720,
		evaluationLevel: project.EvaluationLevelConversation,
	})

	require.NotNil(t, plan.eval.Source)
	assert.Equal(t, 720, plan.eval.Source.LookbackHours)
	assert.Equal(t, 20, plan.eval.Source.MaxTraces)
	assert.Equal(t, project.EvaluationLevelConversation, plan.eval.EvaluationLevel)
}
