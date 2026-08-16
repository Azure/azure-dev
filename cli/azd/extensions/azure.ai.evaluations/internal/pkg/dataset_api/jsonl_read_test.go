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

// One .jsonl per dataset in one folder is the ordinary layout under ./evals.
// Scanning the directory instead of reading the declared file registers the
// rows of whichever sorts first under the other one's name, while the
// reconciler records the fingerprint of the declared file — so the two agree
// forever and the eval scores data nobody chose.
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

	content, err = ReadFirstJSONLFile(dir)
	require.NoError(t, err)
	assert.Contains(t, content, "not me", "the directory form takes the first .jsonl")
}

// The dataset extension strips the BOM before upload; this path uploads the
// same rows under `azd up` and has to agree, or the same file registers
// differently depending on which command sent it.
func TestReadFirstJSONLFile_StripsTheByteOrderMark(t *testing.T) {
	dir := t.TempDir()
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte("{\"query\":\"q\"}\n")...)

	named := filepath.Join(dir, "d.jsonl")
	require.NoError(t, os.WriteFile(named, body, 0o600))

	for _, path := range []string{named, dir} {
		content, err := ReadFirstJSONLFile(path)
		require.NoError(t, err)
		assert.Truef(t, strings.HasPrefix(content, `{"query"`),
			"the first row has to start with its own first key, got %q", content)
	}
}

// A file holding only a BOM has no rows, and registering it succeeds — the
// failure would surface at the run that scores it instead.
func TestReadFirstJSONLFile_RefusesAnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	named := filepath.Join(dir, "d.jsonl")
	require.NoError(t, os.WriteFile(named, []byte{0xEF, 0xBB, 0xBF}, 0o600))

	_, err := ReadFirstJSONLFile(named)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rows")
}

// The reconciler validates rows before it publishes, but `dataset create` and
// `dataset update` do not go through it. Upload does not parse rows either, so
// without this a malformed line registers a version that looks healthy and only
// fails in the run that reads it.
func TestReadFirstJSONLFileRefusesAMalformedRow(t *testing.T) {
	dir := t.TempDir()
	named := filepath.Join(dir, "rows.jsonl")
	body := "{\"query\":\"ok\"}\n{not json}\n"
	require.NoError(t, os.WriteFile(named, []byte(body), 0o600))

	_, err := ReadFirstJSONLFile(named)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "2", "the error has to name the line that is wrong")
}

func TestReadFirstJSONLFileRefusesAnEmptyRow(t *testing.T) {
	dir := t.TempDir()
	named := filepath.Join(dir, "rows.jsonl")
	require.NoError(t, os.WriteFile(named, []byte("{\"query\":\"ok\"}\n{}\n"), 0o600))

	_, err := ReadFirstJSONLFile(named)

	require.Error(t, err)
}
