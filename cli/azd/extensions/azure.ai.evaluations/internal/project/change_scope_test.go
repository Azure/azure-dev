// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/assert"
)

func evalNamed(name, dataset string, evaluators ...string) Eval {
	refs := evalcore.EvaluatorList{}
	for _, e := range evaluators {
		refs = append(refs, evalcore.EvaluatorRef{Evaluator: e})
	}
	return Eval{Name: name, Dataset: dataset, Evaluators: refs}
}

// An eval is immutable, so recreating it abandons the id its runs are recorded
// against. Scenario 3 is a developer comparing this run to the last one under
// that id, and one flag for the whole file used to break that for every eval
// because one unrelated dataset gained a row.
func TestChangeSetOnlyReachesWhatAnEvalNames(t *testing.T) {
	changed := changeSet{
		datasets:   map[string]bool{"support-golden": true, "billing-golden": false},
		evaluators: map[string]bool{"support-quality": false, "billing-quality": false},
	}

	support := evalNamed("support-eval", "support-golden",
		"builtin.task_adherence", "support-quality")
	billing := evalNamed("billing-eval", "billing-golden",
		"builtin.task_adherence", "billing-quality")

	assert.True(t, changed.reaches(support), "its own dataset was republished")
	assert.False(t, changed.reaches(billing),
		"a sibling's dataset changing is not a reason to discard this eval's history")
}

func TestChangeSetReachesThroughAnEvaluator(t *testing.T) {
	changed := changeSet{
		datasets:   map[string]bool{"golden": false},
		evaluators: map[string]bool{"quality": true},
	}

	assert.True(t,
		changed.reaches(evalNamed("e", "golden", "quality")),
		"an evaluator this eval runs was republished")
	assert.False(t,
		changed.reaches(evalNamed("other", "golden", "builtin.task_adherence")),
		"this eval does not run that evaluator")
}

// Built-ins belong to the service. This configuration never publishes one, so
// a name that merely looks like a catalog entry must not recreate anything.
func TestChangeSetIgnoresBuiltins(t *testing.T) {
	changed := changeSet{
		datasets:   map[string]bool{},
		evaluators: map[string]bool{"builtin.task_adherence": true},
	}

	assert.False(t, changed.reaches(evalNamed("e", "golden", "builtin.task_adherence")))
}

// A configuration whose datasets and evaluators are all already registered
// publishes nothing, and must leave every eval where it is.
func TestChangeSetReachesNothingWhenNothingChanged(t *testing.T) {
	changed := changeSet{
		datasets:   map[string]bool{"golden": false},
		evaluators: map[string]bool{"quality": false},
	}

	assert.False(t, changed.reaches(evalNamed("e", "golden", "quality")))
	assert.False(t, changed.reaches(evalNamed("no-dataset", "")))
}
