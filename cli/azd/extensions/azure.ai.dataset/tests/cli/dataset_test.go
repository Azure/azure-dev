// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// datasetSummary is the shape a script consumes from `-o json`.
type datasetSummary struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Type    string `json:"type"`
	DataURI string `json:"dataUri"`
}

const oneRow = `{"query":"reset my password","response":"Use Forgot Password."}` + "\n"

// latestVersion reads back what the service assigned. Versions are "1.0", not
// "1", so a cleanup that guesses deletes nothing and leaves the project dirty.
func latestVersion(t *testing.T, name string) string {
	t.Helper()
	var ds datasetSummary
	requireSuccess(t, run(t, "show", name, "-o", "json")).JSON(t, &ds)
	require.NotEmpty(t, ds.Version)
	return ds.Version
}

// removeDataset deletes every version it can see, so nothing outlives the test.
func removeDataset(t *testing.T, name string) {
	t.Helper()
	var versions []datasetSummary
	res := run(t, "versions", "list", name, "-o", "json")
	if res.ExitCode != 0 {
		return
	}
	if err := json.Unmarshal([]byte(res.Stdout), &versions); err != nil {
		return
	}
	for _, v := range versions {
		run(t, "delete", name, "--version", v.Version)
	}
}

// registerDataset publishes a dataset and removes it when the test ends.
func registerDataset(t *testing.T, rows string) (string, string) {
	t.Helper()

	name := uniqueName("azdcli-ds")
	file := writeRows(t, rows)

	r := requireSuccess(t, run(t, "create", name, "--from-file", file))
	require.Contains(t, r.Combined(), name)

	t.Cleanup(func() { removeDataset(t, name) })
	return name, file
}

// The round trip the extension exists for: register, list, read back.
func TestCLIDatasetLifecycle(t *testing.T) {
	name, file := registerDataset(t, oneRow)

	t.Run("show returns the registered version", func(t *testing.T) {
		var ds datasetSummary
		requireSuccess(t, run(t, "show", name, "-o", "json")).JSON(t, &ds)

		require.Equal(t, name, ds.Name)
		require.NotEmpty(t, ds.Version)
		require.NotEmpty(t, ds.DataURI, "without a URI nothing can read the rows back")
	})

	t.Run("it appears in the listing", func(t *testing.T) {
		var all []datasetSummary
		requireSuccess(t, run(t, "list", "-o", "json")).JSON(t, &all)

		found := false
		for _, d := range all {
			if d.Name == name {
				found = true
			}
		}
		require.True(t, found, "a registered dataset must appear in the listing")
	})

	t.Run("update publishes a further version", func(t *testing.T) {
		first := latestVersion(t, name)
		requireSuccess(t, run(t, "update", name, "--from-file", file))

		second := latestVersion(t, name)
		require.NotEqual(t, first, second,
			"update must advance the version rather than overwrite")

		var versions []datasetSummary
		requireSuccess(t, run(t, "versions", "list", name, "-o", "json")).JSON(t, &versions)
		require.GreaterOrEqual(t, len(versions), 2)
	})

	t.Run("the table names its columns", func(t *testing.T) {
		r := requireSuccess(t, run(t, "list"))
		for _, header := range []string{"NAME", "VERSION", "TYPE"} {
			require.Containsf(t, r.Stdout, header, "the listing lost its %s column", header)
		}
	})
}

// Every list has to be a bare array, or a caller's parsing depends on which
// service envelope happened to come back.
func TestCLIJSONListsAreBareArrays(t *testing.T) {
	for _, args := range [][]string{{"list", "-o", "json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out := requireSuccess(t, run(t, args...)).Stdout
			require.True(t, strings.HasPrefix(strings.TrimSpace(out), "["),
				"a list must emit an array, got:\n%s", out)
		})
	}
}

// A name the service will reject is worth refusing locally: the service answers
// with a 400 wrapped in several levels of JSON, which says nothing useful.
func TestCLIInvalidNameIsRefusedLocally(t *testing.T) {
	file := writeRows(t, oneRow)

	r := requireFailure(t, run(t, "create", "bad name", "--from-file", file))

	require.Contains(t, r.Combined(), "invalid")
	require.NotContains(t, r.Combined(), "RESPONSE 400",
		"the refusal must come before the request")
}

// Pointing at one dataset in a folder holding several must register that one.
// Scanning the directory would upload whichever .jsonl sorts first.
func TestCLIFromFileUploadsTheNamedFile(t *testing.T) {
	dir := t.TempDir()
	chosen := filepath.Join(dir, "zebra.jsonl")
	require.NoError(t, os.WriteFile(chosen, []byte(oneRow), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "alpha.jsonl"),
		[]byte(`{"query":"wrong","response":"wrong"}`+"\n"), 0o600))

	name := uniqueName("azdcli-named")
	requireSuccess(t, run(t, "create", name, "--from-file", chosen))
	t.Cleanup(func() { removeDataset(t, name) })

	var ds datasetSummary
	requireSuccess(t, run(t, "show", name, "-o", "json")).JSON(t, &ds)
	require.NotEmpty(t, ds.DataURI)
}

// A Windows editor writes a BOM by default. Uploaded as-is it becomes part of
// the first row's first key, and nothing fails until something reads that row.
func TestCLIByteOrderMarkIsAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bom.jsonl")
	require.NoError(t, os.WriteFile(path,
		append([]byte{0xEF, 0xBB, 0xBF}, []byte(oneRow)...), 0o600))

	name := uniqueName("azdcli-bom")
	requireSuccess(t, run(t, "create", name, "--from-file", path))
	t.Cleanup(func() { removeDataset(t, name) })
}

// Refused before upload: registering an empty dataset succeeds, and the
// failure then surfaces at whatever tries to read it.
func TestCLIEmptyDatasetIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("   \n"), 0o600))

	r := requireFailure(t, run(t, "create", uniqueName("azdcli-empty"), "--from-file", path))

	require.Contains(t, r.Combined(), "no rows")
}

// A mistyped path is the common way to get here, and the syscall that
// discovered it says nothing to the person who mistyped it.
func TestCLIMissingFileNamesThePath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.jsonl")

	r := requireFailure(t, run(t, "create", uniqueName("azdcli-missing"), "--from-file", missing))

	require.Contains(t, r.Combined(), "does not exist")
	require.NotContains(t, r.Combined(), "GetFileAttributesEx")
}

func TestCLIUnknownDatasetIsBrief(t *testing.T) {
	r := requireFailure(t, run(t, "show", "azdcli-no-such-dataset"))

	require.Contains(t, r.Combined(), "no dataset")
	require.NotContains(t, r.Combined(), "RESPONSE 404",
		"a missing name does not need the whole HTTP body to explain it")
}

// A list is a filter, not a lookup, so an unknown name lists nothing and
// succeeds. `show` is the lookup and still refuses. The eval extension answers
// both the same way, and a caller moving between the two should not have to
// learn which one errors.
func TestCLIVersionsListOfAnUnknownNameSucceeds(t *testing.T) {
	r := requireSuccess(t, run(t, "versions", "list", "azdcli-no-such-dataset", "-o", "json"))

	var versions []map[string]any
	r.JSON(t, &versions)
	require.Empty(t, versions, "an unknown name lists nothing rather than failing")
}

// Succeeding quietly is right for a parser and wrong for a reader. The project
// holds other datasets, so "No datasets found." answers a question nobody
// asked; the line has to be about the name that was typed.
func TestCLIVersionsListOfAnUnknownNameNamesIt(t *testing.T) {
	r := requireSuccess(t, run(t, "versions", "list", "azdcli-no-such-dataset"))

	require.Contains(t, r.Stdout, `No versions of dataset "azdcli-no-such-dataset"`)
	require.NotContains(t, r.Stdout, "No datasets found.",
		"the project has datasets; this name just has no versions")
}

// Required arguments must end the process rather than wait on a terminal
// nobody is watching.
func TestCLIRequiredValuesFailInsteadOfHanging(t *testing.T) {
	cases := [][]string{
		{"create", uniqueName("azdcli-noflag"), "--no-prompt"},
		{"show"},
		{"delete", "some-name"},
	}

	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := requireFailure(t, run(t, args...))
			require.NotEmpty(t, strings.TrimSpace(r.Combined()),
				"a refusal has to say what it could not resolve")
		})
	}
}

// Deleting something that is not registered is how a cleanup script ends, so
// it is not an error.
func TestCLIDeleteIsIdempotent(t *testing.T) {
	requireSuccess(t, run(t, "delete", "azdcli-never-registered", "--version", "1"))
}
