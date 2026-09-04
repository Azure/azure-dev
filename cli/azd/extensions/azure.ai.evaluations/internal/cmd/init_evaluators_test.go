// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
)

// What an eval grades on is a SET, so there is no "the only one" to detect the
// way there is for the target and the judge model. Which criteria define
// quality is the substantive decision in the configuration, so init asks.
//
// The defaults are what it proposes, and they are what --no-prompt takes.
func TestDefaultEvaluatorsProposeABuiltinAndTheRubric(t *testing.T) {
	assert.Equal(t,
		[]string{evalcore.BuiltinPrefix + "task_adherence", "support-agent-quality"},
		defaultEvaluators("support-agent-quality", false))
}

// A trace-backed eval has no target whose instructions a rubric is written
// from, so proposing one would plan a file nothing can generate.
func TestDefaultEvaluatorsSkipTheRubricForTraces(t *testing.T) {
	assert.Equal(t,
		[]string{evalcore.BuiltinPrefix + "task_adherence"},
		defaultEvaluators("support-agent-quality", true))
}

// The prompt offers what is knowable without a service call -- init makes none
// -- which is the pair it proposes plus whatever the catalog already declares.
func TestEvaluatorChoicesOfferTheCatalogToo(t *testing.T) {
	cfg := &project.EvalConfig{Evaluators: []project.EvaluatorDecl{
		{Name: "support-agent-quality"}, // already proposed, must not double up
		{Name: "tone-check"},
	}}

	got := evaluatorChoices(cfg, "support-agent-quality", false)

	assert.Equal(t, []string{
		evalcore.BuiltinPrefix + "task_adherence",
		"support-agent-quality",
		"tone-check",
	}, got)
}
