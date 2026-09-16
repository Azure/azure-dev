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

// An extension's own path keys are rebased like the two core owns.
//
// A path written inside a $ref file is written relative to that file. Spliced
// into a document in another directory it means something else: an evaluator
// pulled in from evaluators/quality.yaml carries `source: ./quality.json`,
// which without rebasing is looked for beside the configuration. Core cannot
// know that `source` is a path, so the owning extension says so.
func TestDeclaredPathKeysAreRebased(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "evaluators", "quality.yaml"),
		[]byte("name: quality\nsource: ./quality.json\n"), 0o600))

	cfg := map[string]any{
		"evaluators": []any{map[string]any{"$ref": "./evaluators/quality.yaml"}},
	}

	resolved, err := ResolveFileRefs(cfg, dir, WithPathKeys("source"))
	require.NoError(t, err)

	entry := resolved["evaluators"].([]any)[0].(map[string]any)
	assert.Equal(t, "evaluators/quality.json", entry["source"],
		"a declared path key is re-anchored to the root the caller named")
}

// Without the declaration the value is left exactly as written, which is the
// behaviour every caller had before the option existed.
func TestUndeclaredKeysAreLeftAlone(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "evaluators"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "evaluators", "quality.yaml"),
		[]byte("name: quality\nsource: ./quality.json\n"), 0o600))

	cfg := map[string]any{
		"evaluators": []any{map[string]any{"$ref": "./evaluators/quality.yaml"}},
	}

	resolved, err := ResolveFileRefs(cfg, dir)
	require.NoError(t, err)

	entry := resolved["evaluators"].([]any)[0].(map[string]any)
	assert.Equal(t, "./quality.json", entry["source"])
}

// A path written inline in the root document is already relative to it, so
// declaring the key must not move it.
func TestInlinePathsAreNotRebased(t *testing.T) {
	dir := t.TempDir()

	cfg := map[string]any{
		"evaluators": []any{map[string]any{
			"name":   "quality",
			"source": "./evaluators/quality.json",
		}},
	}

	resolved, err := ResolveFileRefs(cfg, dir, WithPathKeys("source"))
	require.NoError(t, err)

	entry := resolved["evaluators"].([]any)[0].(map[string]any)
	assert.Equal(t, "./evaluators/quality.json", entry["source"],
		"nothing was spliced, so there is no other directory to rebase from")
}

// Nested includes rebase from the file that actually holds the value, which is
// what recovering a base from the visible $ref cannot do.
func TestNestedIncludesRebaseFromTheirOwnFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a", "b"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "a", "b", "inner.yaml"),
		[]byte("name: quality\nsource: ./rubric.json\n"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "a", "outer.yaml"),
		[]byte("$ref: ./b/inner.yaml\n"), 0o600))

	cfg := map[string]any{
		"evaluators": []any{map[string]any{"$ref": "./a/outer.yaml"}},
	}

	resolved, err := ResolveFileRefs(cfg, dir, WithPathKeys("source"))
	require.NoError(t, err)

	entry := resolved["evaluators"].([]any)[0].(map[string]any)
	assert.Equal(t, "a/b/rubric.json", entry["source"],
		"the value belongs to inner.yaml, two directories down from the root")
}
