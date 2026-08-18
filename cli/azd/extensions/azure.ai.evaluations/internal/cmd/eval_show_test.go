// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
)

// `show` is documented as showing an eval definition, and answered with the id,
// the name and who created it -- true of any eval, and none of the definition.
// The graders are the part of the definition the service does return.
func TestShowSurfacesWhatTheEvalGrades(t *testing.T) {
	group := &eval_api.OpenAIEval{
		ID:   "eval_1",
		Name: "support-trace-eval",
		TestingCriteria: []eval_api.TestingCriterion{
			{Name: "task_adherence", EvaluatorName: "builtin.task_adherence"},
			{Name: "quality", EvaluatorName: "support-quality", EvaluatorVersion: "2"},
		},
	}

	assert.Equal(t, "builtin.task_adherence, support-quality (2)", evalGraders(group))
}

// The criterion label is what the service echoes when no evaluator reference
// was recorded, so it is better than printing nothing.
func TestShowFallsBackToTheCriterionLabel(t *testing.T) {
	group := &eval_api.OpenAIEval{
		TestingCriteria: []eval_api.TestingCriterion{{Name: "custom-grader"}},
	}

	assert.Equal(t, "custom-grader", evalGraders(group))
}

// An older eval, or one the service answers without a definition, still shows
// its identity rather than blank rows.
func TestShowOmitsWhatTheServiceDidNotSend(t *testing.T) {
	assert.Empty(t, evalGraders(&eval_api.OpenAIEval{ID: "eval_1"}))
	assert.Empty(t, evalGraders(nil))

	// A criterion carrying no name at all contributes nothing rather than an
	// empty entry with a stray separator.
	assert.Empty(t, evalGraders(&eval_api.OpenAIEval{
		TestingCriteria: []eval_api.TestingCriterion{{Type: "azure_ai_evaluator"}},
	}))
}
