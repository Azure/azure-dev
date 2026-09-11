// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
)

// Precedence decides behavior without announcing it, so a wrong answer here
// is silent. options.max_samples was parsed and dropped once already, which is
// what these lock down.
func TestResolveMaxSamples_Precedence(t *testing.T) {
	withOptions := &project.Eval{MaxSamples: 25}

	assert.Equal(t, 5, resolveMaxSamples(5, withOptions), "the flag wins over the config")
	assert.Equal(t, 25, resolveMaxSamples(0, withOptions), "the config is used when no flag is given")
	assert.Equal(t, 0, resolveMaxSamples(0, &project.Eval{}), "neither means no cap")
	assert.Equal(t, 0, resolveMaxSamples(0, nil))
	assert.Equal(t, 7, resolveMaxSamples(7, nil), "a flag stands on its own")

	// Zero in config is absent, not a cap of zero: a cap of zero would send
	// nothing at all.
	assert.Equal(t, 0, resolveMaxSamples(0, &project.Eval{MaxSamples: 0}))
}

// The level is the eval's alone. A per-run override would put two incomparable
// result sets under one eval's history, and would bypass the
// supported_evaluation_levels check `azd up` does against the declared level.
func TestResolveLevel_ComesFromTheEval(t *testing.T) {
	declared := &project.Eval{
		EvaluationLevel: project.EvaluationLevelConversation,
	}

	assert.Equal(t, project.EvaluationLevelConversation, resolveLevel(declared))
	assert.Empty(t, resolveLevel(&project.Eval{}), "unset defers to the service default")
	assert.Empty(t, resolveLevel(nil))
}

// A group's target decides which run-time fields its criteria can bind. Getting
// this wrong passes validation and then errors on every row.
func TestSampleBindingsFor_UnknownTargetBindsNothing(t *testing.T) {
	assert.Nil(t, sampleBindingsFor("prompt"),
		"an unrecognized target must bind nothing rather than guess at agent fields")
}

// The level filter is what keeps a conversation evaluator from being sent turn
// fields and the reverse. Both directions matter.
func TestSelectLevelFields_KeepsOnlyTheLevelsShape(t *testing.T) {
	accepted := []string{"query", "response", "messages", "tool_definitions"}

	conv := selectLevelFields(accepted, nil, project.EvaluationLevelConversation)
	assert.Contains(t, conv, "messages")
	assert.NotContains(t, conv, "query")
	assert.NotContains(t, conv, "response")
	assert.Contains(t, conv, "tool_definitions", "fields outside the split are untouched")

	turn := selectLevelFields(accepted, nil, project.EvaluationLevelTurn)
	assert.Contains(t, turn, "query")
	assert.Contains(t, turn, "response")
	assert.NotContains(t, turn, "messages")

	// An evaluator offering only one shape is left alone, whatever the level.
	only := []string{"query", "response"}
	assert.Equal(t, only, selectLevelFields(only, nil, project.EvaluationLevelConversation))

	// A required field is never dropped: a genuine conflict has to surface as a
	// missing-field error rather than being reshaped away.
	kept := selectLevelFields(accepted, []string{"query"}, project.EvaluationLevelConversation)
	assert.Contains(t, kept, "query", "a required field survives the level filter")
}
