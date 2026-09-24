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

// writeLocalDataset drops a JSONL file in a temp dir and returns its path.
func writeLocalDataset(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

// init exits 0 and writes dataset and eval declarations for local JSONL it
// cannot turn into evaluation rows. The file is in its hand and it makes no
// service call, so the failure was deferred to a deploy that had nothing to
// work with. ADO 5631311.
func TestInitScaffold_RefusesLocalDatasetFilesItCannotUse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contents string
		wantErr  string
	}{
		{
			name:     "a line that is not JSON",
			contents: "{\"query\":\"valid first row\"}\nnot-json\n",
			wantErr:  "line 2 is not valid JSON",
		},
		{
			name:     "an empty file",
			contents: "",
			wantErr:  "has no rows to evaluate",
		},
		{
			name:     "a file of only blank lines",
			contents: "\n   \n\n",
			wantErr:  "has no rows to evaluate",
		},
		{
			name:     "an array row instead of an object",
			contents: "[\"query\", \"response\"]\n",
			wantErr:  "line 1 is not valid JSON",
		},
		{
			name:     "an object row that carries nothing",
			contents: "{}\n",
			wantErr:  "line 1 is an empty object",
		},
		{
			name:     "a row that is a bare scalar",
			contents: "\"just a string\"\n",
			wantErr:  "line 1 is not valid JSON",
		},
		{
			name:     "a valid first row followed by an array",
			contents: "{\"query\":\"q\"}\n[\"not\",\"an\",\"object\"]\n",
			wantErr:  "line 2 is not valid JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateJSONL(writeLocalDataset(t, "rows.jsonl", tt.contents))

			require.Error(t, err, "init must refuse this before writing a declaration")
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// The files init has always accepted must keep being accepted. Refusing one of
// these would be a worse regression than the bug.
func TestInitScaffold_AcceptsUsableLocalDatasetFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contents string
	}{
		{
			name:     "one row",
			contents: "{\"query\":\"q\"}\n",
		},
		{
			name:     "several rows",
			contents: "{\"query\":\"a\"}\n{\"query\":\"b\"}\n{\"query\":\"c\"}\n",
		},
		{
			name:     "blank lines between rows are not rows",
			contents: "{\"query\":\"a\"}\n\n{\"query\":\"b\"}\n",
		},
		{
			name:     "no trailing newline",
			contents: "{\"query\":\"a\"}",
		},
		{
			name:     "conversation rows carrying nested messages",
			contents: "{\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}\n",
		},
		{
			name:     "seed rows carrying no query at all",
			contents: "{\"test_case_description\":\"a delayed order\",\"desired_num_turns\":4}\n",
		},
		{
			name: "a byte order mark on the first row",
			// PowerShell redirection writes one, and deploy already tolerates it.
			contents: "\uFEFF{\"query\":\"a\"}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.NoError(t, validateJSONL(writeLocalDataset(t, "rows.jsonl", tt.contents)))
		})
	}
}

// A path with no file behind it is reported as absent rather than as a row
// problem, so the reader is not sent looking inside a file that is not there.
func TestInitScaffold_UnreadableDatasetIsReportedAsAPathProblem(t *testing.T) {
	t.Parallel()

	err := validateJSONL(filepath.Join(t.TempDir(), "missing.jsonl"))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "line",
		"a missing file has no line to report")
}
