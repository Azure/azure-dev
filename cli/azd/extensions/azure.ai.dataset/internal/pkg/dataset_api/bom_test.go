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
