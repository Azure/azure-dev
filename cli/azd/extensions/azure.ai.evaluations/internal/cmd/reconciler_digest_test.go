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

// A code evaluator's published version depends on more than its script, so the
// digest that decides whether to republish has to cover the rest of it.
// Hashing only the source would leave a changed metric or image tag sitting in
// the config while `azd up` reported nothing to do.
func TestCodeEvaluatorDigest(t *testing.T) {
	dir := t.TempDir()

	script := filepath.Join(dir, "grader.py")
	require.NoError(t, os.WriteFile(script,
		[]byte("def grade(sample, item) -> float:\n    return 1.0\n"), 0o600))

	metrics := filepath.Join(dir, "metrics.json")
	require.NoError(t, os.WriteFile(metrics,
		[]byte(`{"result":{"type":"continuous"}}`), 0o600))

	base := project.EvaluatorDecl{Name: "answer_length"}
	baseline, err := codeEvaluatorDigest(base, script)
	require.NoError(t, err)

	t.Run("stable across calls", func(t *testing.T) {
		again, err := codeEvaluatorDigest(base, script)
		require.NoError(t, err)
		require.Equal(t, baseline, again,
			"an unchanged evaluator must not republish on every deploy")
	})

	t.Run("notices the script", func(t *testing.T) {
		require.NoError(t, os.WriteFile(script,
			[]byte("def grade(sample, item) -> float:\n    return 2.0\n"), 0o600))
		changed, err := codeEvaluatorDigest(base, script)
		require.NoError(t, err)
		require.NotEqual(t, baseline, changed)
	})

	t.Run("notices the image tag", func(t *testing.T) {
		withImage := base
		withImage.ImageTag = "python:3.12-slim"
		changed, err := codeEvaluatorDigest(withImage, script)
		require.NoError(t, err)
		require.NotEqual(t, baseline, changed,
			"changing the image changes what runs, so it must republish")
	})

	t.Run("notices the metrics file", func(t *testing.T) {
		withMetrics := base
		withMetrics.Metrics = metrics
		before, err := codeEvaluatorDigest(withMetrics, script)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(metrics,
			[]byte(`{"result":{"type":"ordinal","min_value":0,"max_value":1}}`), 0o600))
		after, err := codeEvaluatorDigest(withMetrics, script)
		require.NoError(t, err)
		require.NotEqual(t, before, after,
			"editing metrics alone must republish, or the edit never deploys")
	})

	t.Run("reports a missing settings file", func(t *testing.T) {
		missing := base
		missing.DataSchema = filepath.Join(dir, "nope.json")
		_, err := codeEvaluatorDigest(missing, script)
		require.Error(t, err)
		require.Contains(t, err.Error(), "data_schema",
			"the error must name which setting could not be read")
	})
}
