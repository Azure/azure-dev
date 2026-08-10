// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The one difference between create and update, and the only thing stopping a
// create from silently publishing version 2 of someone else's dataset.
func TestCheckAssetExistence(t *testing.T) {
	assert.NoError(t, checkAssetExistence("create", "dataset", "x", false))
	assert.NoError(t, checkAssetExistence("update", "dataset", "x", true))

	err := checkAssetExistence("create", "dataset", "x", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update", "the error has to name the verb that works")

	err = checkAssetExistence("update", "dataset", "x", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create")
}

// A mistyped --from-file is the common way to get here, and the syscall that
// discovered it says nothing to the person who mistyped it.
func TestDatasetUploadSourceOnAMissingPath(t *testing.T) {
	_, err := datasetUploadSource(filepath.Join(t.TempDir(), "nope.jsonl"))
	require.Error(t, err)

	assert.Contains(t, err.Error(), "does not exist")
	assert.NotContains(t, err.Error(), "GetFileAttributesEx")
	assert.NotContains(t, err.Error(), "stat ")
}

// Pointing at one dataset in a folder holding several must upload that one.
func TestDatasetUploadSourceKeepsTheNamedFile(t *testing.T) {
	dir := t.TempDir()
	chosen := filepath.Join(dir, "zebra.jsonl")
	require.NoError(t, os.WriteFile(chosen, []byte("{\"pick\":\"me\"}\n"), 0o600))
	// Sorts first, so a directory scan would take it instead.
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "alpha.jsonl"), []byte("{\"pick\":\"not me\"}\n"), 0o600))

	resolved, err := datasetUploadSource(chosen)
	require.NoError(t, err)
	assert.Equal(t, chosen, resolved)

	resolved, err = datasetUploadSource(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, resolved)
}
