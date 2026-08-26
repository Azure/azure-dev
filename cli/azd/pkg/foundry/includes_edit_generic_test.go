// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An extension edits a document of its own shape through this type, so the
// loading, saving and $ref handling stay in one place.
//
// Without it each extension reimplements them, and they drift: one preserves
// comments, another round-trips through typed structs and deletes them.
func TestGenericEditingPreservesCommentsAndIncludes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`# why this eval exists
datasets:
  - name: golden          # the reviewed rows
    file: ./datasets/golden.jsonl

evaluators:
  - $ref: ./evaluators/quality.yaml
`), 0o600))

	doc, err := LoadYAMLDocument(path)
	require.NoError(t, err)
	require.NoError(t, doc.Append("datasets", map[string]any{
		"name": "extra",
		"file": "./datasets/extra.jsonl",
	}))
	require.NoError(t, doc.Save())

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(after)

	assert.Contains(t, text, "# why this eval exists")
	assert.Contains(t, text, "# the reviewed rows")
	assert.Contains(t, text, "$ref: ./evaluators/quality.yaml",
		"an include is a directive to keep, not content to inline")
	assert.Contains(t, text, "name: extra")
}

// A path reaches into the document, so a caller does not walk the node tree.
func TestFindAndSetOnAnArbitraryDocument(t *testing.T) {
	doc, err := ParseYAMLDocument("azure.eval.yaml", []byte(`
evals:
  - name: nightly
    dataset: golden
`))
	require.NoError(t, err)

	node, err := doc.Find("evals[0].dataset")
	require.NoError(t, err)
	assert.Equal(t, "golden", node.Value)

	require.NoError(t, doc.Set("evals[0].dataset", "prod-golden"))
	node, err = doc.Find("evals[0].dataset")
	require.NoError(t, err)
	assert.Equal(t, "prod-golden", node.Value)
}

// An empty document is a document with nothing in it, not a failure: generate
// writes one before it has anything to record.
func TestGenericEditingCreatesAnEmptyDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "azure.eval.yaml")

	doc, err := ParseYAMLDocument(path, nil)
	require.NoError(t, err)

	root, err := doc.Root(false)
	require.NoError(t, err)
	assert.Nil(t, root, "nothing is there yet, and asking is not an edit")

	require.NoError(t, doc.Append("datasets", map[string]any{"name": "golden"}))
	require.NoError(t, doc.Save())

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(after), "name: golden")
}
