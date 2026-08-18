// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Now that init asks rather than defaults, the rubric arrives as an explicit
// choice instead of through the empty-evaluators branch. It still has to be
// generated: the configuration declares a file, and if nothing produces it,
// `create` fails looking for it -- which is finding 0b, reintroduced by the
// prompt if the chosen path did not set this.
func TestScaffoldGeneratesARubricThatWasChosenRatherThanDefaulted(t *testing.T) {
	plan, cfg := scaffoldFor(t, scaffoldInput{
		evalName: "support-agent-eval",
		target:   "support-agent",
		dataset:  "golden",
		evaluators: []string{
			evalcore.BuiltinPrefix + "task_adherence",
			"support-agent-quality",
		},
	})

	assert.True(t, plan.generateRubric,
		"a chosen rubric still has to be generated, or its file never exists")

	require.Len(t, cfg.Evaluators, 1, "only the rubric is a catalog entry; the builtin is not")
	assert.Equal(t, "support-agent-quality", cfg.Evaluators[0].Name)
}

// A built-in is resolved by the service and has no local file, so choosing only
// built-ins must not plan a generation.
func TestScaffoldGeneratesNoRubricForBuiltinsAlone(t *testing.T) {
	plan, cfg := scaffoldFor(t, scaffoldInput{
		evalName:   "support-agent-eval",
		target:     "support-agent",
		dataset:    "golden",
		evaluators: []string{evalcore.BuiltinPrefix + "task_adherence"},
	})

	assert.False(t, plan.generateRubric)
	assert.Empty(t, cfg.Evaluators)
}

// An evaluator the author already has on disk is not the rubric init offers, so
// it is declared without planning a generation over it.
func TestScaffoldDoesNotGenerateAnUnrelatedEvaluator(t *testing.T) {
	plan, cfg := scaffoldFor(t, scaffoldInput{
		evalName:   "support-agent-eval",
		target:     "support-agent",
		dataset:    "golden",
		evaluators: []string{"tone-check"},
	})

	assert.False(t, plan.generateRubric,
		"only the rubric init offers to write is one it knows how to generate")
	require.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "tone-check", cfg.Evaluators[0].Name)
}
