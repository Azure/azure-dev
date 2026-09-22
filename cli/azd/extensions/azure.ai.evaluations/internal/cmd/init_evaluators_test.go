// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What an eval grades on is a SET, so there is no "the only one" to detect the
// way there is for the target and the judge model. Which criteria define
// quality is the substantive decision in the configuration, so init asks.
//
// The default is one built-in. It used to add a rubric generated from the
// agent's instructions, which meant init declared an evaluator file nothing had
// produced yet and `azd up` failed on it.
func TestDefaultEvaluatorsProposeOnlyWhatAlreadyResolves(t *testing.T) {
	assert.Equal(t,
		[]string{evalcore.BuiltinPrefix + "task_completion"},
		defaultEvaluators())
}

func TestInitEvaluatorChoicesRespectKnownLocalCompatibility(t *testing.T) {
	cfg := &project.EvalConfig{Evaluators: []project.EvaluatorDecl{
		{Name: "turn-only", SupportedEvaluationLevels: []string{"turn"}},
		{Name: "conversation-only", SupportedEvaluationLevels: []string{"conversation"}},
		{Name: "unknown"},
		{Name: "future", SupportedEvaluationLevels: []string{"future-level"}},
	}}
	for _, level := range evaluationLevels {
		t.Run(level, func(t *testing.T) {
			choices := evaluatorChoices(cfg, level)
			assert.Contains(t, choices, level+"-only")
			assert.Contains(t, choices, "unknown")
			assert.Contains(t, choices, "future")
			for _, other := range evaluationLevels {
				if other != level {
					assert.NotContains(t, choices, other+"-only")
					require.ErrorContains(t, validateInitEvaluatorLevels(cfg, []string{other + "-only"}, level),
						"--evaluation-level "+level)
				}
			}
			assert.NoError(t, validateInitEvaluatorLevels(cfg, []string{"unknown", "future"}, level))
		})
	}
}

// Four options, one ticked. Preselecting more decided for the author what
// quality means for their agent, which is the substantive choice in the file.
func TestEvaluatorChoicesOfferTheFourBuiltins(t *testing.T) {
	assert.Equal(t, []string{
		evalcore.BuiltinPrefix + "task_completion",
		evalcore.BuiltinPrefix + "customer_satisfaction",
		evalcore.BuiltinPrefix + "coherence",
		evalcore.BuiltinPrefix + "groundedness",
	}, evaluatorChoices(nil, project.EvaluationLevelTurn))
}

// The prompt offers what is knowable without a service call -- the picker makes
// none -- which is the four built-ins plus whatever the catalog already
// declares. A declaration is offered because its file already exists; nothing
// that would have to be generated first appears here.
func TestEvaluatorChoicesOfferTheCatalogToo(t *testing.T) {
	cfg := &project.EvalConfig{Evaluators: []project.EvaluatorDecl{
		{Name: "support-agent-quality"},
		{Name: "tone-check"},
	}}

	got := evaluatorChoices(cfg, project.EvaluationLevelTurn)

	assert.Equal(t, []string{
		evalcore.BuiltinPrefix + "task_completion",
		evalcore.BuiltinPrefix + "customer_satisfaction",
		evalcore.BuiltinPrefix + "coherence",
		evalcore.BuiltinPrefix + "groundedness",
		"support-agent-quality",
		"tone-check",
	}, got)
}
