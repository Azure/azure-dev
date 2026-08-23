// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scaffolding an eval edits the file instead of rewriting it.
//
// `init` used to marshal the whole decoded configuration back, which deleted
// every comment the author had written and dropped any key the structs do not
// model. It now writes only the entries it decided to add.
func TestScaffoldingAnEvalLeavesTheRestOfTheFileAlone(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase), []byte(`# Owned by the support team.
datasets:
  # curated by hand -- do not regenerate
  - name: golden
    file: ./datasets/golden.jsonl

evals:
  - name: existing
    dataset: golden
    target:
      $ref: ./parts/target.yaml
`), 0o600))

	require.NoError(t, ApplyScaffold(dir, ScaffoldWrite{
		Evaluators: []EvaluatorDecl{{Name: "quality", Source: "./evaluators/quality.json"}},
		Evals: []Eval{{
			Name:       "nightly",
			Dataset:    "golden",
			Evaluators: evalcore.EvaluatorList{{Evaluator: "quality"}},
		}},
	}))

	got, err := os.ReadFile(filepath.Join(dir, EvalConfigBase))
	require.NoError(t, err)
	body := string(got)

	assert.Contains(t, body, "# Owned by the support team.")
	assert.Contains(t, body, "# curated by hand -- do not regenerate")
	assert.Contains(t, body, "$ref: ./parts/target.yaml",
		"a directive on a shape these structs do not model survives")

	assert.Contains(t, body, "name: nightly")
	assert.Contains(t, body, "name: quality")
	assert.Contains(t, body, "name: existing", "the eval that was already there is untouched")
}

// `--force` replaces one eval and leaves its neighbours alone.
func TestForcingAnEvalReplacesOnlyThatEntry(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase), []byte(`evals:
  - name: keep-me
    dataset: golden
  - name: nightly
    dataset: stale
    max_samples: 5
`), 0o600))

	require.NoError(t, ApplyScaffold(dir, ScaffoldWrite{
		RemoveEval: "nightly",
		Evals:      []Eval{{Name: "nightly", Dataset: "golden"}},
	}))

	cfg, err := OpenEvalConfigForEdit(dir)
	require.NoError(t, err)
	require.Len(t, cfg.Evals, 2, "replaced, not appended beside itself")

	names := []string{cfg.Evals[0].Name, cfg.Evals[1].Name}
	assert.Contains(t, names, "keep-me")
	assert.Contains(t, names, "nightly")

	replaced, err := cfg.Eval("nightly")
	require.NoError(t, err)
	assert.Equal(t, "golden", replaced.Dataset)
	assert.Zero(t, replaced.MaxSamples, "the replaced entry does not keep the old one's keys")
}

// A scaffold that decided to add nothing does not touch the file at all.
func TestScaffoldingNothingDoesNotRewriteTheFile(t *testing.T) {
	original := "evals:\n  - name: nightly\n    dataset: golden\n"
	dir := t.TempDir()
	path := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))

	require.NoError(t, ApplyScaffold(dir, ScaffoldWrite{}))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, original, string(after), "untouched, byte for byte")
}
