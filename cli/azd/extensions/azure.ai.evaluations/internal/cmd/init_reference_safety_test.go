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

// init writes only references that already resolve.
//
// It used to declare `./evaluators/<name>.json` for an evaluator it had never
// seen and leave generation to produce the file. The eval then graded on a
// rubric that did not exist: `azd up` deployed the project and the agent, then
// failed on the eval service. Generation is a separate command with its own
// cost and confirmation, so init refuses rather than promising.
func TestScaffoldRefusesAnEvaluatorNothingHasDeclared(t *testing.T) {
	_, err := planScaffold(scaffoldInput{
		evalName:   "support-agent-eval",
		target:     "support-agent",
		dataset:    "golden",
		evalDir:    project.DefaultEvalDir,
		cfg:        &project.EvalConfig{},
		evaluators: []string{"support-agent-quality"},
	})

	require.Error(t, err, "a rubric nothing has produced is not a reference init may write")
	assert.Contains(t, err.Error(), "support-agent-quality")
	assert.Contains(t, err.Error(), "generate")
}

// One already declared is a reference that resolves, so the eval may grade on
// it. This is the path that keeps `init` usable after `generate` has run.
func TestScaffoldAcceptsAnAlreadyDeclaredEvaluator(t *testing.T) {
	cfg := &project.EvalConfig{
		Datasets:   []project.DatasetDecl{{Name: "golden"}},
		Evaluators: []project.EvaluatorDecl{{Name: "support-agent-quality", Source: "./evaluators/q.json"}},
	}

	plan, _ := scaffoldFor(t, scaffoldInput{
		evalName:   "support-agent-eval",
		target:     "support-agent",
		dataset:    "golden",
		cfg:        cfg,
		evaluators: []string{"support-agent-quality"},
	})

	require.Len(t, plan.eval.Evaluators, 1)
	assert.Equal(t, "support-agent-quality", plan.eval.Evaluators[0].Evaluator)
	require.Len(t, cfg.Evaluators, 1, "the declaration it already had is not duplicated")
}

// A built-in is resolved by the service and has no local file, so it never
// needs a declaration.
func TestScaffoldAcceptsBuiltinsWithoutADeclaration(t *testing.T) {
	_, cfg := scaffoldFor(t, scaffoldInput{
		evalName:   "support-agent-eval",
		target:     "support-agent",
		dataset:    "golden",
		evaluators: []string{evalcore.BuiltinPrefix + "task_adherence"},
	})

	assert.Empty(t, cfg.Evaluators, "a builtin is not a catalog entry")
}

// The same rule on the dataset half, which is the one that produced the
// reported failure: init named `./datasets/<eval>.jsonl` and generation was
// expected to fill it in later.
func TestScaffoldRefusesToInventADataset(t *testing.T) {
	_, err := planScaffold(scaffoldInput{
		evalName: "support-agent-eval",
		target:   "support-agent",
		evalDir:  project.DefaultEvalDir,
		cfg:      &project.EvalConfig{},
	})

	require.Error(t, err, "a dataset nothing has produced is not a reference init may write")
	assert.Contains(t, err.Error(), "--dataset")
}

// The one declaration in the file is not a guess, so it is used without asking.
func TestScaffoldUsesTheOnlyDeclaredDataset(t *testing.T) {
	cfg := &project.EvalConfig{Datasets: []project.DatasetDecl{{Name: "support-regression"}}}

	plan, _ := scaffoldFor(t, scaffoldInput{
		evalName: "support-agent-eval",
		target:   "support-agent",
		cfg:      cfg,
	})

	assert.Equal(t, "support-regression", plan.eval.Dataset)
	require.Len(t, cfg.Datasets, 1, "an existing declaration is referenced, not re-added")
}

// Several declarations have no single right answer, and picking one silently
// would grade against a dataset the author did not mean.
func TestScaffoldRefusesToChooseAmongDeclaredDatasets(t *testing.T) {
	_, err := planScaffold(scaffoldInput{
		evalName: "support-agent-eval",
		target:   "support-agent",
		evalDir:  project.DefaultEvalDir,
		cfg: &project.EvalConfig{Datasets: []project.DatasetDecl{
			{Name: "support-regression"},
			{Name: "hero-tests"},
		}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "support-regression")
	assert.Contains(t, err.Error(), "hero-tests", "the refusal has to name the choices")
}
