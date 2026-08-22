// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A command that writes the configuration back reads it as written.
//
// `init` and `generate` read, modify and save the same file. Handing them a
// resolved configuration inlined the author's includes, orphaned the files they
// named, and left the paths inside those files resolving against the wrong
// directory: a `source: ./quality.json` written beside `evaluators/quality.yaml`
// came back pointing at the project root. Nothing reported it, because from the
// writer's point of view it had saved what it read.
func TestEditingReadsLeaveIncludesAlone(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "evaluators", "quality.yaml"),
		[]byte("name: quality\nsource: ./quality.json\n"), 0o600))

	path := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(path, []byte(`datasets:
  - name: golden
    file: ./datasets/golden.jsonl

evaluators:
  - $ref: ./evaluators/quality.yaml

evals:
  - name: nightly
    dataset: golden
`), 0o600))

	cfg, err := OpenEvalConfigForEdit(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NoError(t, SaveEvalConfig(dir, cfg))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(after)

	assert.Contains(t, text, "$ref: ./evaluators/quality.yaml",
		"the author's include has to survive a command that saves the file")
	assert.NotContains(t, text, "source: ./quality.json",
		"inlining it would leave that path resolving against the wrong directory")
}

// The reader that commands *use* still resolves, so the two do not drift apart.
func TestConsumingReadsStillResolve(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "evaluators", "quality.yaml"),
		[]byte("name: quality\nsource: ./quality.json\n"), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase), []byte(`evaluators:
  - $ref: ./evaluators/quality.yaml

evals:
  - name: nightly
`), 0o600))

	cfg, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	require.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "quality", cfg.Evaluators[0].Name)
}

// An include is the only thing the editing reader treats differently. A
// configuration without one decodes identically either way, so a mistyped key
// is still refused on the path that writes.
func TestEditingReadsAreStillStrict(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase),
		[]byte("datasets:\n  - name: golden\n    fiel: ./x.jsonl\n"), 0o600))

	_, err := OpenEvalConfigForEdit(dir)

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "fiel"),
		"the typo has to be named on the path that saves the file too")
}
