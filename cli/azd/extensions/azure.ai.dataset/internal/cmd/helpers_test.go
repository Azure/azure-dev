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
func TestDatasetUploadDirOnAMissingPath(t *testing.T) {
	_, err := datasetUploadDir(filepath.Join(t.TempDir(), "nope.jsonl"))
	require.Error(t, err)

	assert.Contains(t, err.Error(), "does not exist")
	assert.NotContains(t, err.Error(), "GetFileAttributesEx")
	assert.NotContains(t, err.Error(), "stat ")
}

func TestDatasetUploadDirResolvesWhatWasNamed(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "rows.jsonl")
	require.NoError(t, os.WriteFile(file, []byte("{}\n"), 0o600))

	resolved, err := datasetUploadDir(file)
	require.NoError(t, err)
	assert.Equal(t, dir, resolved, "a file resolves to the directory the upload scans")

	resolved, err = datasetUploadDir(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, resolved)
}
