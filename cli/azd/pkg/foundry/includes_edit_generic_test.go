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

// Replacing a value keeps the comments the author put on it.
//
// yamlnode.Set swaps in the newly encoded node, which drops the head, line and
// foot comments the old one carried. Losing them defeats the point of editing
// the document in place rather than re-encoding it.
func TestSetKeepsCommentsOnTheValueItReplaces(t *testing.T) {
	doc, err := ParseYAMLDocument("azure.eval.yaml", []byte(`
evals:
  - name: nightly
    dataset: golden # the reviewed rows
`))
	require.NoError(t, err)
	require.NoError(t, doc.Set("evals[0].dataset", "prod-golden"))

	out, err := doc.Bytes()
	require.NoError(t, err)

	assert.Contains(t, string(out), "prod-golden")
	assert.Contains(t, string(out), "# the reviewed rows",
		"the comment describes the field, which still exists")
}

// Set does not invent the parents of the path it is given.
//
// Creation is yamlnode's `?` qualifier, and staying with it keeps one meaning
// for a path across the codebase rather than two.
func TestSetRequiresTheQualifierToCreateAParent(t *testing.T) {
	doc, err := ParseYAMLDocument("azure.eval.yaml", []byte("name: demo\n"))
	require.NoError(t, err)

	require.Error(t, doc.Set("catalog.dataset", "golden"),
		"an unmarked path names a node that has to be there already")

	require.NoError(t, doc.Set("catalog?.dataset", "golden"))
	out, err := doc.Bytes()
	require.NoError(t, err)
	assert.Contains(t, string(out), "dataset: golden")
}

// An index one past the end is out of bounds, not an append.
//
// The bounds check admitted it and then indexed it, so setting evals[1] against
// a single-element sequence panicked instead of returning an error.
func TestSetRejectsAnIndexPastTheEnd(t *testing.T) {
	doc, err := ParseYAMLDocument("azure.eval.yaml", []byte(`
evals:
  - name: nightly
`))
	require.NoError(t, err)

	err = doc.Set("evals[1]", map[string]any{"name": "weekly"})
	require.Error(t, err, "one past the end is Append's job")
	assert.Contains(t, err.Error(), "out of bounds")

	require.NoError(t, doc.Set("evals[0]", map[string]any{"name": "weekly"}))
}
