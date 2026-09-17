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

// A named file has to be uploaded as itself.
//
// This resolver used to return the file's DIRECTORY, and the upload then took
// whichever .jsonl sorted first. Pointing --from-file at one dataset in a
// folder holding several therefore registered a different one under that name,
// and because the fingerprint described the file that was named, the two agreed
// with each other forever afterwards. The sibling dataset extension was fixed;
// this copy was not, so the same command uploaded different bytes depending on
// which namespace the user typed.
func TestDatasetUploadSourceReadsTheFileThatWasNamed(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a-sorts-first.jsonl")
	wanted := filepath.Join(dir, "b-is-the-one-named.jsonl")
	require.NoError(t, os.WriteFile(first, []byte(`{"query":"a"}`), 0o600))
	require.NoError(t, os.WriteFile(wanted, []byte(`{"query":"b"}`), 0o600))

	got, err := datasetUploadSource(wanted)

	require.NoError(t, err)
	assert.Equal(t, wanted, got, "the file that was named, not the one that sorts first")
}

// A directory is offered by the flag, so one .jsonl inside it resolves.
func TestDatasetUploadSourceResolvesADirectoryHoldingOne(t *testing.T) {
	dir := t.TempDir()
	only := filepath.Join(dir, "only.jsonl")
	require.NoError(t, os.WriteFile(only, []byte(`{"query":"a"}`), 0o600))

	got, err := datasetUploadSource(dir)

	require.NoError(t, err)
	assert.Equal(t, only, got)
}

// Several is not "a directory containing one", and picking would be a guess.
func TestDatasetUploadSourceRefusesAnAmbiguousDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte(`{}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.jsonl"), []byte(`{}`), 0o600))

	_, err := datasetUploadSource(dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "a.jsonl")
	assert.Contains(t, err.Error(), "b.jsonl", "naming them is what makes the refusal actionable")
}

func TestDatasetUploadSourceRefusesADirectoryWithNoJSONL(t *testing.T) {
	_, err := datasetUploadSource(t.TempDir())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no .jsonl file")
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

// The service refuses a bad name with a 400 wrapping four levels of JSON, so
// the guard exists to say it plainly. The sibling extension had it; this copy,
// which serves the same commands under `azd ai eval dataset`, did not.
func TestValidAssetNameMatchesTheSibling(t *testing.T) {
	for _, ok := range []string{"golden", "a_b-c", "A1"} {
		assert.Truef(t, validAssetName(ok), "%q is a name the service accepts", ok)
	}
	for _, bad := range []string{"", "has space", "slash/name", "dots.here", "uni\u00e9"} {
		assert.Falsef(t, validAssetName(bad), "%q must be refused before the round trip", bad)
	}
}
