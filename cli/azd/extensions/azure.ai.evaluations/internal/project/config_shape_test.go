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

// A configuration of the wrong shape used to be replaced rather than reported.
//
// readConfigDocument only unmarshals into a node -- nothing checks the shape --
// and documentMapping then overwrote a scalar or sequence root with an empty
// mapping. UpsertCatalogEntry serialized that and wrote it back, so a file the
// author had written by hand was erased by a command that only meant to add one
// entry. The comment there claimed the read path reported it first; on this path
// there is no read path.
func TestAWronglyShapedConfigIsRefusedRatherThanErased(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"a sequence at the root", "- not\n- a\n- configuration\n"},
		{"a scalar at the root", "just a string\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, EvalConfigBase)
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))

			_, _, err := UpsertCatalogEntry(dir, SectionDatasets, "rows", "version", "1.0")

			require.Error(t, err, "a file this cannot edit must be reported, not rewritten")

			after, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			assert.Equal(t, tc.body, string(after),
				"the author's file has to survive a command that refused to edit it")
		})
	}
}

// The same guard must not refuse a catalog the author spelled as empty: `evals:`
// with nothing under it is a list with no entries, not a wrong shape.
func TestAnEmptyCatalogKeyStillAcceptsEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EvalConfigBase)
	require.NoError(t, os.WriteFile(path, []byte("datasets:\n"), 0o600))

	changed, _, err := UpsertCatalogEntry(dir, SectionDatasets, "rows", "version", "1.0")

	require.NoError(t, err)
	assert.True(t, changed)

	after, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Contains(t, string(after), "rows")
}

// A catalog that is present but is not a list is the erasing case again, one
// level down.
func TestACatalogThatIsNotAListIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EvalConfigBase)
	body := "datasets: this is not a list\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	_, _, err := UpsertCatalogEntry(dir, SectionDatasets, "rows", "version", "1.0")

	require.Error(t, err)

	after, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, body, string(after), "nothing may be discarded to make room")
}
