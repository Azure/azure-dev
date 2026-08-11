// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cli

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type datasetSummary struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Format  string `json:"format"`
}

const datasetRows = `{"query":"How do I reset my password?"}
{"query":"What is the refund window?"}
`

// registeredDataset is a dataset with more than one version, which is what
// makes --version on show and --name on list worth asserting.
type registeredDataset struct {
	Name string
	// Versions are read back from each registration rather than assumed to
	// start at 1: the server assigns them, and a test that hardcoded the
	// numbering would be asserting its own guess.
	Versions []string
}

var (
	readOnlyDatasetOnce sync.Once
	readOnlyDataset     *registeredDataset
)

// sharedDataset is registered once for the tests that only read it. Each
// registration uploads a blob, so redoing it per test buys nothing.
func sharedDataset(t *testing.T) *registeredDataset {
	t.Helper()
	readOnlyDatasetOnce.Do(func() {
		readOnlyDataset = registerDataset(t, 2)
	})
	require.NotNil(t, readOnlyDataset, "the shared dataset could not be registered")
	return readOnlyDataset
}

// registerDataset publishes a dataset and removes every version it created.
func registerDataset(t *testing.T, versions int) *registeredDataset {
	t.Helper()
	require.Positive(t, versions)

	path := filepath.Join(t.TempDir(), "golden.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(datasetRows), 0o600))

	ds := &registeredDataset{Name: uniqueName("azdcli_ds")}
	for i := range versions {
		// The first publish is a create; every later one is an update, which is
		// the only difference between them.
		verb := "update"
		if i == 0 {
			verb = "create"
		}
		r := requireSuccess(t, run(t, "dataset", verb,
			ds.Name, "--from-file", path, "-o", "json"))

		var created datasetSummary
		r.JSON(t, &created)
		require.NotEmpty(t, created.Version, "the service assigns the version")
		ds.Versions = append(ds.Versions, created.Version)

		version := created.Version
		deferTeardown(func() {
			runQuietly("dataset", "delete", ds.Name, "--version", version)
		})
	}
	require.Len(t, ds.Versions, versions)
	require.NotEqual(t, ds.Versions[0], ds.Versions[len(ds.Versions)-1],
		"updating must advance the version rather than overwrite")
	return ds
}

func TestCLIDatasetList(t *testing.T) {
	ds := sharedDataset(t)

	t.Run("table", func(t *testing.T) {
		r := requireSuccess(t, run(t, "dataset", "versions", "list", ds.Name))
		// TYPE, not FORMAT. The service populates `type` (`uri_file`) and leaves
		// `format` empty, so the column this once pinned was blank on every row.
		for _, header := range []string{"NAME", "VERSION", "TYPE"} {
			require.Containsf(t, r.Stdout, header, "the listing lost its %s column", header)
		}
		require.Contains(t, r.Stdout, ds.Name)
	})

	// `versions list` is what makes the listing usable once a project holds more
	// than a screenful: it narrows to one dataset's versions.
	t.Run("versions list scopes to one dataset's versions", func(t *testing.T) {
		r := requireSuccess(t, run(t, "dataset", "versions", "list", ds.Name, "-o", "json"))
		var listed []datasetSummary
		r.JSON(t, &listed)
		require.NotEmpty(t, listed)

		seen := map[string]bool{}
		for _, v := range listed {
			require.Equalf(t, ds.Name, v.Name,
				"the listing must return only that dataset's versions; got %q", v.Name)
			seen[v.Version] = true
		}
		for _, want := range ds.Versions {
			require.Truef(t, seen[want], "version %s is missing from the listing", want)
		}
	})

	// Unscoped, the listing is every dataset rather than every version, so the
	// one just registered has to be in it.
	t.Run("unscoped lists the project's datasets", func(t *testing.T) {
		r := requireSuccess(t, run(t, "dataset", "list", "-o", "json"))
		var all []datasetSummary
		r.JSON(t, &all)
		require.NotEmpty(t, all)

		found := false
		for _, d := range all {
			if d.Name == ds.Name {
				found = true
			}
		}
		require.True(t, found, "a registered dataset must appear in the unscoped listing")
	})

	t.Run("an unknown name lists nothing rather than failing", func(t *testing.T) {
		r := requireSuccess(t, run(t, "dataset", "versions", "list",
			"azdcli-no-such-dataset", "-o", "json"))
		var listed []datasetSummary
		r.JSON(t, &listed)
		require.Empty(t, listed)
	})
}

func TestCLIDatasetShow(t *testing.T) {
	ds := sharedDataset(t)
	latest := ds.Versions[len(ds.Versions)-1]

	// Omitting the version means the latest, which is the only sensible
	// default for a name that gains a version on every registration.
	t.Run("defaults to the latest version", func(t *testing.T) {
		r := requireSuccess(t, run(t, "dataset", "show", ds.Name, "-o", "json"))
		var shown datasetSummary
		r.JSON(t, &shown)
		require.Equal(t, ds.Name, shown.Name)
		require.Equal(t, latest, shown.Version)
	})

	t.Run("version pins an earlier one", func(t *testing.T) {
		r := requireSuccess(t, run(t, "dataset", "show",
			ds.Name, "--version", ds.Versions[0], "-o", "json"))
		var shown datasetSummary
		r.JSON(t, &shown)
		require.Equal(t, ds.Versions[0], shown.Version)
		require.NotEqual(t, latest, shown.Version)
	})

	t.Run("table", func(t *testing.T) {
		r := requireSuccess(t, run(t, "dataset", "show", ds.Name))
		// `show` reads one dataset, so it renders a detail view keyed by label
		// rather than a one-row table with a header. The labels are what is
		// under test; the column-header form belongs to `dataset list`.
		for _, label := range []string{"Name", "Version", "URI"} {
			require.Containsf(t, r.Stdout, label, "the detail view lost its %s line", label)
		}
		require.Contains(t, r.Stdout, ds.Name)
	})

	t.Run("the name is required", func(t *testing.T) {
		r := requireFailure(t, run(t, "dataset", "show"))
		require.Contains(t, r.Combined(), "accepts 1 arg")
	})

	t.Run("an unknown dataset is brief", func(t *testing.T) {
		r := requireFailure(t, run(t, "dataset", "show", "azdcli-no-such-dataset"))
		require.Less(t, len(r.Combined()), 600,
			"a not-found must stay short, not dump the service body:\n%s", r.Combined())
		require.Contains(t, r.Combined(), "azdcli-no-such-dataset")
	})

	t.Run("an unknown version of a real dataset is refused", func(t *testing.T) {
		r := requireFailure(t, run(t, "dataset", "show",
			ds.Name, "--version", "9999"))
		require.Contains(t, r.Combined(), "9999")
		require.Less(t, len(r.Combined()), 600, r.Combined())
	})
}

func TestCLIDatasetDelete(t *testing.T) {
	t.Run("the name and version are both required", func(t *testing.T) {
		require.Contains(t,
			requireFailure(t, run(t, "dataset", "delete", "--version", "1")).Combined(),
			"accepts 1 arg")
		require.Contains(t,
			requireFailure(t, run(t, "dataset", "delete", "whatever")).Combined(),
			"--version is required")
	})

	// Deleting something that was never registered succeeds. The service
	// treats DELETE as idempotent and answers 204 whatever the name, so the
	// command reports a removal it did not perform — and the not-found branch
	// in `dataset delete` cannot be reached this way. Asserted rather than
	// wished away, because a caller scripting against the exit code is
	// entitled to know it means "gone", not "was there and is now gone".
	t.Run("deleting an unregistered dataset is idempotent, not an error", func(t *testing.T) {
		r := requireSuccess(t, run(t, "dataset", "delete",
			"azdcli-no-such-dataset", "--version", "1"))
		require.Contains(t, r.Stdout, "Deleted dataset")

		listed := requireSuccess(t, run(t, "dataset", "versions", "list",
			"azdcli-no-such-dataset", "-o", "json"))
		var remaining []datasetSummary
		listed.JSON(t, &remaining)
		require.Empty(t, remaining, "nothing was there to delete in the first place")
	})

	// A successful delete answers 204 No Content, so asserting the exit code
	// is what catches a client that reads an empty body as a failure and
	// reports a removal it just performed as an error.
	t.Run("one version is removed and the other survives", func(t *testing.T) {
		ds := registerDataset(t, 2)
		gone, kept := ds.Versions[0], ds.Versions[1]

		r := requireSuccess(t, run(t, "dataset", "delete",
			ds.Name, "--version", gone))
		require.Contains(t, r.Stdout, "Deleted dataset")
		require.Contains(t, r.Stdout, ds.Name)

		listed := requireSuccess(t, run(t, "dataset", "versions", "list",
			ds.Name, "-o", "json"))
		var remaining []datasetSummary
		listed.JSON(t, &remaining)

		versions := map[string]bool{}
		for _, v := range remaining {
			versions[v.Version] = true
		}
		require.False(t, versions[gone], "the deleted version must leave the listing")
		require.True(t, versions[kept], "deleting one version must not remove the others")
	})
}
