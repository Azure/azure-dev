// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Recording an artifact edits the file instead of rewriting it.
//
// The catalog commands used to decode the configuration, change a field, and
// marshal the whole thing back. That deleted every comment the author had
// written, reindented the file, and dropped any key the structs did not model.
func TestRecordingAnArtifactLeavesTheRestOfTheFileAlone(t *testing.T) {
	original := `# Nightly quality gate. Owned by the support team.
datasets:
  # golden set, curated by hand -- do not regenerate
  - name: golden
    file: ./datasets/golden.jsonl

evaluators:
  - name: quality
    source: ./evaluators/quality.json   # rubric lives here

evals:
  - name: nightly
    dataset: golden
    target:
      $ref: ./parts/target.yaml
    evaluators:
      - evaluator: quality
`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase), []byte(original), 0o600))

	changed, created, err := UpsertCatalogEntry(dir, "datasets", "regression", "file", "./datasets/regression.jsonl")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.False(t, created, "the file was already there")

	after, err := os.ReadFile(filepath.Join(dir, EvalConfigBase))
	require.NoError(t, err)
	got := string(after)

	assert.Contains(t, got, "# Nightly quality gate. Owned by the support team.",
		"the author's header survives an edit")
	assert.Contains(t, got, "# golden set, curated by hand -- do not regenerate",
		"a comment inside a sequence survives")
	assert.Contains(t, got, "# rubric lives here",
		"a trailing comment on a value survives")
	assert.Contains(t, got, "$ref: ./parts/target.yaml",
		"a directive on a shape these structs do not model survives, which is the whole point")

	assert.Contains(t, got, "name: regression")
	assert.Contains(t, got, "file: ./datasets/regression.jsonl")
}

// The same edit against a configuration the decoder can open still round-trips
// into the shape the rest of the extension reads.
//
// Kept separate from the case above because that one deliberately carries a
// `$ref` under `target:`, which the editing read still refuses -- the write no
// longer destroys it, but modelling it is a specification change. See
// docs/KNOWN-GAPS.md.
func TestRecordingAnArtifactStillProducesAReadableConfiguration(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase), []byte(`# Owned by the support team.
datasets:
  # curated by hand
  - name: golden
    file: ./datasets/golden.jsonl
`), 0o600))

	changed, _, err := UpsertCatalogEntry(dir, "datasets", "regression", "file", "./datasets/regression.jsonl")
	require.NoError(t, err)
	require.True(t, changed)

	cfg, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 2)
	assert.Equal(t, "golden", cfg.Datasets[0].Name)
	assert.Equal(t, "regression", cfg.Datasets[1].Name)

	after, err := os.ReadFile(filepath.Join(dir, EvalConfigBase))
	require.NoError(t, err)
	assert.Contains(t, string(after), "# curated by hand")
}

// An entry that already says what the command was going to write is left alone,
// so a repeated generate does not churn the file.
func TestRecordingWhatIsAlreadyThereChangesNothing(t *testing.T) {
	original := `datasets:
  - name: golden
    file: ./datasets/golden.jsonl
`
	dir := t.TempDir()
	path := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))

	changed, _, err := UpsertCatalogEntry(dir, "datasets", "golden", "file", "./datasets/golden.jsonl")
	require.NoError(t, err)
	assert.False(t, changed, "nothing moved, so nothing should be written")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, original, string(after), "the file is untouched, byte for byte")
}

// A regenerated artifact that moved updates the entry in place.
func TestAnArtifactThatMovedUpdatesItsEntry(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase), []byte(`
evaluators:
  - name: quality
    source: ./old/quality.json
`), 0o600))

	changed, _, err := UpsertCatalogEntry(dir, "evaluators", "quality", "source", "./evaluators/quality.json")
	require.NoError(t, err)
	assert.True(t, changed)

	cfg, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	require.Len(t, cfg.Evaluators, 1, "updated in place, not appended beside itself")
	assert.Equal(t, "./evaluators/quality.json", cfg.Evaluators[0].Source)
}

// A configuration that does not exist yet is created holding only the catalog.
func TestRecordingIntoAProjectWithNoConfigurationYet(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evals")

	changed, created, err := UpsertCatalogEntry(dir, "datasets", "golden", "file", "./datasets/golden.jsonl")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.True(t, created, "generate runs before init on the golden path")

	cfg, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)
	assert.Equal(t, "golden", cfg.Datasets[0].Name)
	assert.Empty(t, cfg.Evals, "the file it creates stays inert until init wires one")
}
