// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeInitDataset puts a dataset file on disk and returns its path.
func writeInitDataset(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rows.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// localDatasetScaffold is the input `init` builds for a local --dataset, with
// the parts the dataset cases do not vary filled in.
func localDatasetScaffold(t *testing.T, datasetPath string) scaffoldInput {
	t.Helper()
	return scaffoldInput{
		evalName: "quality",
		target:   "support-agent",
		source:   initSourceDataset,
		dataset:  datasetPath,
		evalDir:  t.TempDir(),
		cfg:      &project.EvalConfig{},
	}
}

// `init` used to scaffold cleanly over a dataset file it could not turn into
// evaluation rows, exit 0, and leave the failure for a deploy that names a
// different step.
//
// The validation lives inside planScaffold, so it is driven from there rather
// than by calling validateJSONL directly: a test that calls the validator
// proves the validator works, not that init runs it, and would stay green if
// the call site were deleted.
func TestPlanScaffoldRefusesDatasetRowsInitCannotUse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{"a malformed line", "{\"query\":\"a\"}\n{not json}\n"},
		{"an empty file", ""},
		{"only blank lines", "\n\n  \n"},
		{"an array where an object belongs", "[{\"query\":\"a\"}]\n"},
		{"a bare scalar", "\"just a string\"\n"},
		{"a truncated final row", "{\"query\":\"a\"}\n{\"query\":\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := localDatasetScaffold(t, writeInitDataset(t, tt.body))
			_, err := planScaffold(in)

			require.Error(t, err, "init accepted rows it cannot evaluate")
			// Nothing is written on the way out: the configuration handed in
			// has to come back untouched, or a refused init still leaves a
			// declaration behind.
			assert.Empty(t, in.cfg.Evals, "a refused scaffold declared an eval anyway")
			assert.Empty(t, in.cfg.Datasets, "a refused scaffold declared a dataset anyway")
		})
	}
}

// A path that names nothing is the same broken reference, caught before the
// row check because there is nothing to read.
func TestPlanScaffoldRefusesADatasetPathThatNamesNothing(t *testing.T) {
	t.Parallel()

	in := localDatasetScaffold(t, filepath.Join(t.TempDir(), "absent.jsonl"))
	_, err := planScaffold(in)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "absent.jsonl", "the error names the file that is missing")
}

// The other direction, so the refusals above are a deliberate bar rather than
// planScaffold failing on every local dataset.
func TestPlanScaffoldAcceptsUsableDatasetRows(t *testing.T) {
	t.Parallel()

	body := "{\"query\":\"where is my order\",\"response\":\"it shipped\"}\n" +
		"\n" +
		"{\"query\":\"how do I return it\",\"response\":\"start here\"}\n"

	in := localDatasetScaffold(t, writeInitDataset(t, body))
	out, err := planScaffold(in)

	require.NoError(t, err, "blank lines between rows are not a malformed file")
	require.NotNil(t, out.eval)
	assert.Equal(t, "quality", out.eval.Name)
	assert.Equal(t, "rows", out.datasetName, "the declaration is named after the file")
}

// A bare --dataset is an already-registered dataset name, not a path, so there
// is no file to read and nothing to refuse.
func TestPlanScaffoldDoesNotValidateARegisteredDatasetName(t *testing.T) {
	t.Parallel()

	in := localDatasetScaffold(t, "support-golden")
	out, err := planScaffold(in)

	require.NoError(t, err, "a registered name is not a file init has to be able to read")
	assert.Equal(t, "support-golden", out.datasetName)
}
