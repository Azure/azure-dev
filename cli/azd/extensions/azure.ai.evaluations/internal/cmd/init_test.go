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
	depPath := filepath.Join(dir, "azure.yaml")

	cfg := buildDeployScaffold("support-agent", "support-agent-quality", "", nil, "gpt-4.1-nano")
	require.NoError(t, writeYAML(depPath, cfg))

	loaded, err := project.LoadEvalConfig(depPath)
	require.NoError(t, err)
	require.NoError(t, loaded.Validate(), "the generated scaffold must be valid")

	g, err := loaded.ResolveGroup("")
	require.NoError(t, err)
	require.Equal(t, project.TargetTypeAgent, g.Target.Type)
	require.Equal(t, "support-agent", g.Target.Name)
	require.Equal(t, "gpt-4.1-nano", g.Options.EvalModel)
	require.Len(t, g.Evaluators, 1)
}

func TestGenerateScaffold_RoundTripsAndValidates(t *testing.T) {
	dir := t.TempDir()
	genPath := filepath.Join(dir, "eval_generate.yaml")

	cfg := buildGenerateScaffold("support-agent", "support-agent-quality", "gpt-4.1-nano")
	require.NoError(t, writeYAML(genPath, cfg))

	loaded, err := project.LoadGenerateConfig(genPath)
	require.NoError(t, err)
	require.NoError(t, loaded.Validate())
	require.Equal(t, "support-agent", loaded.Agent.Name)
	require.Equal(t, project.StrategySynthetic, loaded.Generate.Dataset.Strategy)
	require.Equal(t, project.DefaultSampleSize, loaded.Generate.Dataset.SampleSize)
}

// Built-ins are referenced from the group but never declared as custom
// evaluators; declaring one is a validation error.
func TestScaffold_BuiltinEvaluatorsAreNotDeclared(t *testing.T) {
	cfg := buildDeployScaffold(
		"support-agent", "unused", "",
		[]string{"builtin.task_adherence", "my-custom"}, "",
	)

	require.Len(t, cfg.Evaluators, 1, "only the custom evaluator should be declared")
	require.Equal(t, "my-custom", cfg.Evaluators[0].Name)

	require.Len(t, cfg.EvalGroups[0].Evaluators, 2)
	require.True(t, cfg.EvalGroups[0].Evaluators[0].IsBuiltin())
	require.False(t, cfg.EvalGroups[0].Evaluators[1].IsBuiltin())

	path := filepath.Join(t.TempDir(), "azure.yaml")
	require.NoError(t, writeYAML(path, cfg))
	loaded, err := project.LoadEvalConfig(path)
	require.NoError(t, err)
	require.NoError(t, loaded.Validate())
}

// A bare name means an already-registered dataset; a path means a local file.
func TestScaffold_DatasetReferenceForms(t *testing.T) {
	t.Run("local path becomes a source", func(t *testing.T) {
		cfg := buildDeployScaffold("a", "r", "./tests/golden.jsonl", nil, "")
		require.Equal(t, "./tests/golden.jsonl", cfg.Datasets[0].Source)
		require.Equal(t, "golden", cfg.Datasets[0].Name)
	})

	t.Run("bare name references a registered dataset", func(t *testing.T) {
		cfg := buildDeployScaffold("a", "r", "prod-sample", nil, "")
		require.Equal(t, "prod-sample", cfg.Datasets[0].Name)
		require.Empty(t, cfg.Datasets[0].Source,
			"a registered dataset must not get a local source")
	})

	t.Run("no dataset flag scaffolds a local path", func(t *testing.T) {
		cfg := buildDeployScaffold("support-agent", "r", "", nil, "")
		require.Contains(t, cfg.Datasets[0].Source, "support-agent-golden.jsonl")
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
	nested := filepath.Join(dir, "evals", "azure.yaml")

	require.NoError(t, writeYAML(nested, &project.EvalConfig{}))
	_, err := os.Stat(nested)
	require.NoError(t, err, "the file must land exactly at the requested path")

	doubled := filepath.Join(dir, "evals", "evals", "azure.yaml")
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
