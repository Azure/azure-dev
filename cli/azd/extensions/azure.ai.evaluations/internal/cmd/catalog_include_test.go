// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A name declared through a `$ref` is refused, not appended alongside.
//
// The editing read sees the directive rather than the entry behind it, so the
// duplicate scan had nothing to match on and appended a second entry with the
// same name. The collision then surfaced on the next resolving read, naming a
// duplicate the author never wrote and could not see in the file in front of
// them.
func TestGenerateRefusesANameAnIncludeAlreadyDeclares(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "quality.yaml"),
		[]byte("name: quality\nsource: ./quality.json\n"), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(`
evaluators:
  - $ref: ./parts/quality.yaml
`), 0o600))

	err := checkCatalogEntryIsEditable(
		dir, mustOpenForEdit(t, dir), "evaluator", "quality")

	require.Error(t, err, "the name is taken, even though this file does not show it")
	assert.Contains(t, err.Error(), "quality")
	assert.Contains(t, err.Error(), "$ref", "the reader has to be told where the name lives")
}

// A name nothing declares is still free, so generation is not blocked by the
// mere presence of an include elsewhere.
func TestGenerateStillAddsANameNobodyDeclares(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "quality.yaml"),
		[]byte("name: quality\nsource: ./quality.json\n"), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(`
evaluators:
  - $ref: ./parts/quality.yaml
`), 0o600))

	require.NoError(t, checkCatalogEntryIsEditable(
		dir, mustOpenForEdit(t, dir), "evaluator", "tone"))
}

// An include carrying an overlay `name` is refused too, even though the name is
// right there in the file.
//
// This is the shape the README recommends for a rubric. Updating it in place
// writes `source:` beside the directive, and resolution then yields both a
// spliced rubric and a source -- a catalog the next read rejects for declaring
// the rubric twice. The name being visible is what made this the easier one to
// miss.
func TestGenerateRefusesAnIncludeThatCarriesItsName(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "evaluators", "quality.json"),
		[]byte(`{"type":"rubric","dimensions":[{"id":"tone","weight":3}]}`), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(`
evaluators:
  - $ref: ./evaluators/quality.json
    name: quality
`), 0o600))

	err := checkCatalogEntryIsEditable(
		dir, mustOpenForEdit(t, dir), "evaluator", "quality")

	require.Error(t, err, "the entry is an include, so it cannot be updated in place")
	assert.Contains(t, err.Error(), "quality")
}

// The dataset branch of the guard is its own lookup, so it gets its own tests.
//
// Every case above is an evaluator, and `catalogEntryShapeOf` dispatches on kind
// before it looks anything up. A regression in the dataset branch would
// reintroduce the duplicate entries the guard exists to prevent while the
// evaluator tests stayed green.
func TestGenerateRefusesADatasetNameAnIncludeAlreadyDeclares(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "golden.yaml"),
		[]byte("name: golden\nfile: ./datasets/golden.jsonl\n"), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(`
datasets:
  - $ref: ./parts/golden.yaml
`), 0o600))

	err := checkCatalogEntryIsEditable(
		dir, mustOpenForEdit(t, dir), "dataset", "golden")

	require.Error(t, err, "the dataset name is taken by the included file")
	assert.Contains(t, err.Error(), "golden")
	assert.Contains(t, err.Error(), "dataset", "the message names the kind it refused")
}

// A dataset include carrying an overlay `name`, the shape the evaluator test
// above covers, refused through the dataset branch.
func TestGenerateRefusesADatasetIncludeThatCarriesItsName(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "golden.yaml"),
		[]byte("file: ./datasets/golden.jsonl\n"), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(`
datasets:
  - $ref: ./parts/golden.yaml
    name: golden
`), 0o600))

	err := checkCatalogEntryIsEditable(
		dir, mustOpenForEdit(t, dir), "dataset", "golden")

	require.Error(t, err, "the entry is an include, so it cannot be updated in place")
	assert.Contains(t, err.Error(), "golden")
}

// An evaluator already carrying its rubric under `definition:` is refused.
//
// No include is involved. Recording a generated file against it writes
// `source:` into an entry that already holds a `definition:`, and the next read
// rejects the whole configuration for declaring the rubric twice -- after the
// generation job has been billed and the file written. Refusing here is what
// keeps the failure ahead of the cost.
func TestGenerateRefusesAnEvaluatorThatAlreadyCarriesItsRubric(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(`
evaluators:
  - name: quality
    definition:
      type: rubric
      dimensions:
        - id: tone
          weight: 3
`), 0o600))

	err := checkCatalogEntryIsEditable(
		dir, mustOpenForEdit(t, dir), "evaluator", "quality")

	require.Error(t, err, "there is nowhere to record a file without declaring the rubric twice")
	assert.Contains(t, err.Error(), "quality")
	assert.Contains(t, err.Error(), "definition", "the reader has to be told which half is already there")
}

// An entry written out here, with no include and no inline rubric, stays
// editable -- the case the guard must not catch.
func TestGenerateStillUpdatesAnEntryWrittenInThisFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(`
datasets:
  - name: golden
    file: ./datasets/golden.jsonl
evaluators:
  - name: quality
    source: ./evaluators/quality.json
`), 0o600))

	cfg := mustOpenForEdit(t, dir)
	require.NoError(t, checkCatalogEntryIsEditable(dir, cfg, "evaluator", "quality"))
	require.NoError(t, checkCatalogEntryIsEditable(dir, cfg, "dataset", "golden"))
}

// An evaluator pinned to a registered version is refused, for the same reason
// an inline rubric is: there is nowhere to record the generated file.
//
// A pin says the rubric already lives in the service. Writing `source:` beside
// it leaves the entry claiming both, which the next read rejects -- again after
// the job has been billed and the file written.
func TestGenerateRefusesAnEvaluatorPinnedToAVersion(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(`
evaluators:
  - name: quality
    version: "3"
`), 0o600))

	err := checkCatalogEntryIsEditable(
		dir, mustOpenForEdit(t, dir), "evaluator", "quality")

	require.Error(t, err, "a pin and a file in one entry is refused on the next read")
	assert.Contains(t, err.Error(), "quality")
	assert.Contains(t, err.Error(), "version", "the reader has to be told what is already there")
}

// A dataset may carry a file and a version together -- the version says which
// one to publish -- so the pin must not make it uneditable.
func TestGenerateStillUpdatesAVersionedDataset(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(`
datasets:
  - name: golden
    file: ./datasets/golden.jsonl
    version: "4"
`), 0o600))

	require.NoError(t, checkCatalogEntryIsEditable(
		dir, mustOpenForEdit(t, dir), "dataset", "golden"))
}

func mustOpenForEdit(t *testing.T, dir string) *project.EvalConfig {
	t.Helper()
	cfg, err := project.OpenEvalConfigForEdit(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	return cfg
}
