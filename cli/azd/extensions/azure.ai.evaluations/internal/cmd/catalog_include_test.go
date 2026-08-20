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
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "quality.yaml"),
		[]byte("name: quality\nsource: ./quality.json\n"), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(`
evaluators:
  - $ref: ./parts/quality.yaml
`), 0o600))

	err := checkNameNotBehindAnInclude(
		dir, mustOpenForEdit(t, dir), "evaluator", "quality")

	require.Error(t, err, "the name is taken, even though this file does not show it")
	assert.Contains(t, err.Error(), "quality")
	assert.Contains(t, err.Error(), "$ref", "the reader has to be told where the name lives")
}

// A name nothing declares is still free, so generation is not blocked by the
// mere presence of an include elsewhere.
func TestGenerateStillAddsANameNobodyDeclares(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "quality.yaml"),
		[]byte("name: quality\nsource: ./quality.json\n"), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(`
evaluators:
  - $ref: ./parts/quality.yaml
`), 0o600))

	require.NoError(t, checkNameNotBehindAnInclude(
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
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "evaluators", "quality.json"),
		[]byte(`{"type":"rubric","dimensions":[{"id":"tone","weight":3}]}`), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(`
evaluators:
  - $ref: ./evaluators/quality.json
    name: quality
`), 0o600))

	err := checkNameNotBehindAnInclude(
		dir, mustOpenForEdit(t, dir), "evaluator", "quality")

	require.Error(t, err, "the entry is an include, so it cannot be updated in place")
	assert.Contains(t, err.Error(), "quality")
}

func mustOpenForEdit(t *testing.T, dir string) *project.EvalConfig {
	t.Helper()
	cfg, err := project.OpenEvalConfigForEdit(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	return cfg
}
