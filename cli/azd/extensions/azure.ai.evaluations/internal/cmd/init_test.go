// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/require"
)

// The scaffold must load and validate cleanly, otherwise `azd up` fails on a
// config the tool itself produced.
func TestScaffold_RoundTripsAndValidates(t *testing.T) {
	dir := t.TempDir()
	evalPath := filepath.Join(dir, "support-agent-smoke.yaml")

	plan := planScaffold("support-agent-smoke", "support-agent", "support-agent-quality",
		"", nil, "gpt-4.1-nano", project.DefaultEvalDir)
	require.NoError(t, writeYAML(evalPath, plan.eval))

	loaded, err := project.LoadEvalConfig(evalPath)
	require.NoError(t, err)
	require.NoError(t, loaded.Validate(), "the generated scaffold must be valid")

	require.Equal(t, project.TargetTypeAgent, loaded.Target.Type)
	require.Equal(t, "support-agent", loaded.Target.Name)
	require.Equal(t, project.DefaultSampleSize, loaded.Options.MaxSamples)

	// The eval takes its name from the file, which is the azure.yaml service key.
	require.Equal(t, "support-agent-smoke", loaded.Eval("support-agent-smoke").Name)
}

// The default set is a built-in plus a generated rubric: the built-in alone
// would be generic, and the rubric is what makes the baseline about this agent.
func TestScaffold_DefaultEvaluators(t *testing.T) {
	plan := planScaffold("support-agent-smoke", "support-agent", "support-agent-quality",
		"", nil, "gpt-5.6-luna", project.DefaultEvalDir)

	require.Equal(t,
		[]string{"builtin.task_adherence", "support-agent-quality"},
		plan.evaluatorNames())
	require.Contains(t, plan.evaluatorSummary(), "support-agent-quality (rubric)")

	// Every evaluator carries the judge deployment, because built-ins declare
	// deployment_name as required and an eval that leaves it off is rejected.
	for _, ref := range plan.eval.Evaluators {
		require.Equal(t, "gpt-5.6-luna", ref.InitializationParameters["deployment_name"],
			"%s must name a judge deployment", ref.Name)
	}
}

// Passing --evaluator replaces the defaults, which is how a caller opts out of
// rubric generation.
func TestScaffold_ExplicitEvaluatorsOptOutOfGeneration(t *testing.T) {
	plan := planScaffold("smoke", "support-agent", "support-agent-quality", "",
		[]string{"builtin.task_adherence"}, "m", project.DefaultEvalDir)

	require.Equal(t, []string{"builtin.task_adherence"}, plan.evaluatorNames())
	require.Zero(t, plan.rubricCount(), "no rubric is generated when evaluators are given")
	require.Empty(t, plan.generate.Evaluator)
}

// `init` closes by naming what to run next, and only what has something to do.
// Pointing a caller who supplied their own artifacts at a generation command
// would submit a billed job for something they already have.
func TestScaffold_NextStepsOfferOnlyWhatIsScheduled(t *testing.T) {
	t.Run("nothing supplied", func(t *testing.T) {
		plan := planScaffold("support-agent-smoke", "support-agent", "support-agent-quality",
			"", nil, "m", project.DefaultEvalDir)
		require.Equal(t, []string{
			"azd ai eval dataset generate support-agent-smoke",
			"azd ai eval evaluator generate support-agent-quality",
		}, plan.nextSteps())
	})

	t.Run("dataset supplied", func(t *testing.T) {
		plan := planScaffold("smoke", "support-agent", "support-agent-quality",
			"prod-golden", nil, "m", project.DefaultEvalDir)
		require.Equal(t,
			[]string{"azd ai eval evaluator generate support-agent-quality"},
			plan.nextSteps())
	})

	t.Run("everything supplied", func(t *testing.T) {
		plan := planScaffold("smoke", "support-agent", "support-agent-quality",
			"prod-golden", []string{"builtin.task_adherence"}, "m", project.DefaultEvalDir)
		require.Equal(t, []string{"azd up", "azd ai eval run start"}, plan.nextSteps(),
			"with every artifact in place the next step is to deploy")
	})
}

func TestGenerateScaffold_RoundTripsAndValidates(t *testing.T) {
	dir := t.TempDir()
	genPath := filepath.Join(dir, "generate.yaml")

	plan := planScaffold("support-agent-smoke", "support-agent", "support-agent-quality",
		"", nil, "gpt-4.1-nano", project.DefaultEvalDir)
	require.NoError(t, writeYAML(genPath, plan.generate))

	loaded, err := project.LoadGenerateConfig(genPath)
	require.NoError(t, err)
	require.Equal(t, "gpt-4.1-nano", loaded.GenerationModel)

	ds, ok := loaded.DatasetSpec("support-agent-smoke")
	require.True(t, ok, "the generation spec is keyed by artifact name")
	require.Equal(t, project.DefaultSampleSize, ds.SampleSize)
	require.Equal(t, "support-agent", ds.DeriveFrom)

	ev, ok := loaded.EvaluatorSpec("support-agent-quality")
	require.True(t, ok)
	require.Equal(t, "./"+project.DefaultEvaluatorsDir, ev.OutputDir)
}

// Built-ins are referenced but never published, so the scaffold must not give
// one a local source to upload.
func TestScaffold_BuiltinEvaluatorsHaveNoSource(t *testing.T) {
	plan := planScaffold("smoke", "support-agent", "unused", "",
		[]string{"builtin.task_adherence", "my-custom"}, "", project.DefaultEvalDir)
	cfg := plan.eval

	require.Len(t, cfg.Evaluators, 2)
	require.True(t, cfg.Evaluators[0].IsBuiltin())
	require.Empty(t, cfg.Evaluators[0].Source)
	require.False(t, cfg.Evaluators[1].IsBuiltin())
	require.NotEmpty(t, cfg.Evaluators[1].Source)

	require.Len(t, cfg.CustomEvaluators(), 1,
		"only the custom evaluator is this config's to publish")

	path := filepath.Join(t.TempDir(), "smoke.yaml")
	require.NoError(t, writeYAML(path, cfg))
	loaded, err := project.LoadEvalConfig(path)
	require.NoError(t, err)
	require.NoError(t, loaded.Validate())
}

// A bare name means an already-registered dataset; a path means a local file.
// Either way the dataset was supplied, so nothing is scheduled to generate it —
// only a missing --dataset produces a generation entry.
func TestScaffold_DatasetReferenceForms(t *testing.T) {
	t.Run("local path becomes a source", func(t *testing.T) {
		// --dataset is relative to the working directory, but source: is
		// resolved relative to the eval config, so it has to be rebased.
		plan := planScaffold("smoke", "a", "r", "./tests/golden.jsonl", nil, "", "evals")
		require.Equal(t, "../tests/golden.jsonl", plan.eval.Dataset.Source,
			"a dataset outside the eval dir must be reached with ..")
		require.Equal(t, "golden", plan.eval.Dataset.Name)
		require.Empty(t, plan.generate.Dataset,
			"a supplied dataset must not be scheduled for generation")
	})

	t.Run("bare name references a registered dataset", func(t *testing.T) {
		plan := planScaffold("smoke", "a", "r", "prod-sample", nil, "", project.DefaultEvalDir)
		require.Equal(t, "prod-sample", plan.eval.Dataset.Name)
		require.Empty(t, plan.eval.Dataset.Source,
			"a registered dataset must not get a local source")
		require.Empty(t, plan.generate.Dataset)
	})

	t.Run("no dataset flag scaffolds a local path and a generation entry", func(t *testing.T) {
		plan := planScaffold("support-agent-smoke", "support-agent", "r", "",
			nil, "", project.DefaultEvalDir)
		require.Equal(t, "support-agent-smoke", plan.eval.Dataset.Name,
			"the dataset is named after the eval")
		require.Contains(t, plan.eval.Dataset.Source, "support-agent-smoke.jsonl")
		require.Contains(t, plan.generate.Dataset, "support-agent-smoke")
	})
}

func TestLooksLikeLocalDataset(t *testing.T) {
	require.True(t, looksLikeLocalDataset("./data/golden.jsonl"))
	require.True(t, looksLikeLocalDataset("golden.jsonl"))
	require.True(t, looksLikeLocalDataset(`data\golden.jsonl`))
	require.False(t, looksLikeLocalDataset("prod-sample"))
}

// Paths are used verbatim relative to the working directory; the doubling bug
// in the agent-scoped command must not reappear.
func TestWriteYAML_UsesPathVerbatim(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "evals", "smoke.yaml")

	require.NoError(t, writeYAML(nested, &project.EvalConfig{}))
	_, err := os.Stat(nested)
	require.NoError(t, err, "the file must land exactly at the requested path")

	doubled := filepath.Join(dir, "evals", "evals", "smoke.yaml")
	_, err = os.Stat(doubled)
	require.Error(t, err, "the path must not be re-rooted under itself")
}

// normalizeRubricBody accepts a bare definition or a full document.
func TestNormalizeRubricBody(t *testing.T) {
	t.Run("bare definition is wrapped", func(t *testing.T) {
		body, err := normalizeRubricBody("quality",
			[]byte(`{"type":"rubric","dimensions":[{"id":"q","weight":10}]}`))
		require.NoError(t, err)
		require.Contains(t, string(body), `"name":"quality"`)
		require.Contains(t, string(body), `"definition"`)
	})

	t.Run("full document keeps its definition and takes the flag name", func(t *testing.T) {
		body, err := normalizeRubricBody("renamed",
			[]byte(`{"name":"old","definition":{"type":"rubric","dimensions":[]}}`))
		require.NoError(t, err)
		require.Contains(t, string(body), `"name":"renamed"`)
	})

	t.Run("rejects a document with neither", func(t *testing.T) {
		_, err := normalizeRubricBody("x", []byte(`{"unrelated":true}`))
		require.ErrorContains(t, err, "dimensions")
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		_, err := normalizeRubricBody("x", []byte(`not json`))
		require.ErrorContains(t, err, "not valid JSON")
	})
}
