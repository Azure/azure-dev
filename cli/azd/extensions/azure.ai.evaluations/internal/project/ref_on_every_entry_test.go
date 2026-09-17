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

// Core resolves `$ref` on every node, not just evaluator entries, so a dataset
// or an eval can be pulled in from its own file too.
//
// Only the evaluator entry modelled the directive, so a configuration that
// deployed and ran fine could not be opened by `init`, `generate` or the catalog
// writers at all: they read the file exactly as written, and the strict decoder
// refused the very key that pointed at the content.
func TestEveryEntryCoreCanSpliceCanAlsoBeEdited(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts"), 0o750))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "golden.yaml"),
		// Written beside this file, and rebased to mean that: `file` is one of the
		// path keys this extension declares with WithPathKeys.
		[]byte("name: golden\nfile: ./datasets/golden.jsonl\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "nightly.yaml"),
		[]byte("name: nightly\ndataset: golden\n"), 0o600))

	path := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(path, []byte(`
datasets:
  - $ref: ./parts/golden.yaml

evals:
  - $ref: ./parts/nightly.yaml
`), 0o600))

	resolved, err := LoadEvalConfig(path)
	require.NoError(t, err, "the resolving route has always accepted this")
	require.Len(t, resolved.Datasets, 1)
	assert.Equal(t, "golden", resolved.Datasets[0].Name)
	assert.Equal(t, filepath.ToSlash(filepath.Join("parts", "datasets", "golden.jsonl")),
		filepath.ToSlash(resolved.Datasets[0].File),
		"the spliced path is rebased onto the directory it was written in")
	require.Len(t, resolved.Evals, 1)
	assert.Equal(t, "nightly", resolved.Evals[0].Name)

	authored, err := ReadAuthoredConfig(dir)
	require.NoError(t, err,
		"a command that edits the file has to be able to open what azd up deploys")
	assert.True(t, authored.HasUnnamedRef(SectionDatasets),
		"the include survives an authored read, so appending writes it back")
	assert.True(t, authored.HasUnnamedRef(SectionEvals))
}
