// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A BOM uploaded as-is becomes part of the first row's first key, so every
// consumer of the dataset sees one malformed record — and nothing fails until
// something tries to read that row.
func TestReadFirstJSONLFile_StripsTheByteOrderMark(t *testing.T) {
	dir := t.TempDir()
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte("{\"query\":\"q\"}\n")...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "d.jsonl"), body, 0o600))

	content, err := ReadFirstJSONLFile(dir)

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(content, `{"query"`),
		"the first row has to start with its own first key, got %q", content)
}

func TestReadFirstJSONLFile_LeavesOrdinaryContentAlone(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "d.jsonl"), []byte("{\"query\":\"q\"}\n"), 0o600))

	content, err := ReadFirstJSONLFile(dir)

	require.NoError(t, err)
	assert.Equal(t, "{\"query\":\"q\"}\n", content)
}

// A file holding nothing but a BOM is still empty, and registering an empty
// dataset only fails later at the run that scores it.
func TestReadFirstJSONLFile_BOMOnlyFileIsStillEmpty(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "d.jsonl"), []byte{0xEF, 0xBB, 0xBF}, 0o600))

	_, err := ReadFirstJSONLFile(dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rows")
}

// One .jsonl per dataset in one folder is the ordinary layout. Scanning the
// directory instead of reading the named file registers the rows of whichever
// sorts first under the other one's name.
func TestReadFirstJSONLFile_ReadsTheNamedFileNotItsNeighbour(t *testing.T) {
	dir := t.TempDir()
	named := filepath.Join(dir, "zebra.jsonl")
	require.NoError(t, os.WriteFile(named, []byte("{\"pick\":\"me\"}\n"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "alpha.jsonl"), []byte("{\"pick\":\"not me\"}\n"), 0o600))

	content, err := ReadFirstJSONLFile(named)
	require.NoError(t, err)
	assert.Contains(t, content, `"me"`)
	assert.NotContains(t, content, "not me")

	// A directory still scans, which is what --from-file <dir> means.
	content, err = ReadFirstJSONLFile(dir)
	require.NoError(t, err)
	assert.Contains(t, content, "not me", "the directory form takes the first .jsonl")
}

// The BOM and empty-file guards have to apply to the named-file path too.
func TestReadFirstJSONLFile_NamedFileGetsTheSameGuards(t *testing.T) {
	dir := t.TempDir()

	withBOM := filepath.Join(dir, "bom.jsonl")
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte("{\"query\":\"q\"}\n")...)
	require.NoError(t, os.WriteFile(withBOM, body, 0o600))

	content, err := ReadFirstJSONLFile(withBOM)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(content, `{"query"`), "got %q", content)

	empty := filepath.Join(dir, "empty.jsonl")
	require.NoError(t, os.WriteFile(empty, []byte("  \n"), 0o600))

	_, err = ReadFirstJSONLFile(empty)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rows")
}
