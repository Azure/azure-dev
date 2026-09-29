// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The entry names come from the service's listing, so they are not this
// command's to trust. An absolute path or one climbing out with `..` writes
// wherever it says, and the destination is chosen by the caller precisely so
// that it does not.
func TestSafeJoinRefusesEntriesThatLeaveTheDirectory(t *testing.T) {
	root := t.TempDir()

	for _, name := range []string{
		"../escaped.jsonl",
		"../../etc/passwd",
		"nested/../../escaped.jsonl",
		"/absolute.jsonl",
		`\absolute.jsonl`,
		"..",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := safeJoin(root, name)
			require.Error(t, err, "%q would be written outside %s", name, root)
			assert.Contains(t, err.Error(), "outside")
		})
	}
}

// A dataset holding a directory of files still has to land under the
// destination, keeping the layout it had.
func TestSafeJoinKeepsRelativeLayout(t *testing.T) {
	root := t.TempDir()

	for _, name := range []string{"rows.jsonl", "nested/rows.jsonl", "./rows.jsonl"} {
		got, err := safeJoin(root, name)
		require.NoError(t, err, name)
		assert.True(t, strings.HasPrefix(got, root),
			"%q resolved to %q, which is not under the download directory", name, got)
	}
}

// A download replaces nothing it was not told to.
//
// The path it writes is derived from the dataset name and version, so it can
// collide with a file the caller wrote themselves -- and overwriting that
// silently is data loss the command was never asked to perform.
func TestDownloadRefusesToOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "golden-1.0.jsonl")
	require.NoError(t, os.WriteFile(existing, []byte("mine\n"), 0o600))

	err := refuseExisting(existing, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force")

	require.NoError(t, refuseExisting(existing, true))
	require.NoError(t, refuseExisting(filepath.Join(dir, "absent.jsonl"), false))

	body, err := os.ReadFile(existing) //nolint:gosec // this test's own temp dir
	require.NoError(t, err)
	assert.Equal(t, "mine\n", string(body), "the refusal must not have touched it")
}

// A file arrives whole or not at all: a partial write under the name a reader
// trusts is the one failure a later run cannot detect.
func TestWriteFileAtomicallyLeavesNothingBehindOnFailure(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "rows.jsonl")

	require.Error(t, writeFileAtomically(dest, failingReader{}, false))
	_, err := os.Stat(dest)
	assert.True(t, os.IsNotExist(err), "a failed write must not leave the destination behind")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "and must not leave its temporary file behind either")

	require.NoError(t, writeFileAtomically(dest, strings.NewReader("{}\n"), false))
	body, err := os.ReadFile(dest) //nolint:gosec // this test's own temp dir
	require.NoError(t, err)
	assert.Equal(t, "{}\n", string(body))
}

// appearingReader creates path partway through being read, which is the window
// between the caller's last existence check and the rename that installs the
// download.
type appearingReader struct {
	t    *testing.T
	path string
	rest io.Reader
	done bool
}

func (r *appearingReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		require.NoError(r.t, os.WriteFile(r.path, []byte("not yours\n"), 0o600))
	}
	return r.rest.Read(p)
}

// Every existence check a caller can run happens before a transfer that may
// take minutes, and the install used to rename over whatever it found by then.
// Without --force the promise has to hold at the moment of replacing, not only
// when the command started.
func TestWriteFileAtomicallyWillNotReplaceAFileThatAppearedDuringTheCopy(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "rows.jsonl")

	body := &appearingReader{t: t, path: dest, rest: strings.NewReader("{}\n")}
	err := writeFileAtomically(dest, body, false)

	require.Error(t, err, "the name was taken by the time the download was ready to land")
	assert.Contains(t, err.Error(), "--force")

	kept, readErr := os.ReadFile(dest) //nolint:gosec // this test's own temp dir
	require.NoError(t, readErr)
	assert.Equal(t, "not yours\n", string(kept), "the file that appeared is untouched")
}

// --force is the caller saying to replace it, so the same race resolves the
// other way.
func TestWriteFileAtomicallyReplacesWhenForced(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "rows.jsonl")

	body := &appearingReader{t: t, path: dest, rest: strings.NewReader("{}\n")}
	require.NoError(t, writeFileAtomically(dest, body, true))

	kept, err := os.ReadFile(dest) //nolint:gosec // this test's own temp dir
	require.NoError(t, err)
	assert.Equal(t, "{}\n", string(kept))
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, assert.AnError }
