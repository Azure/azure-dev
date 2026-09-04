// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A file this command will not upload is refused before the first request.
//
// The rows were only parsed once the upload reached them, which is past the
// read that decides whether the name is taken. So a malformed --from-file was
// reported as whatever the network said first: point it at an endpoint that
// does not resolve and the answer was a DNS failure, naming a host the author
// had not mistyped, about a file they had. Nothing about the request depends on
// the rows, so there is no reason to send it before reading them.
func TestRowsAreReadBeforeAnythingIsSent(t *testing.T) {
	// Ensures the ordering is what passes this test: were the network reached
	// first, it would fail here rather than upload.
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://dataset-tests.invalid/api/projects/none")

	t.Run("a malformed row names the file and the line", func(t *testing.T) {
		err := createFrom(t, "{\"query\":\"a\"}\nthis is not json\n")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "rows.jsonl", "the author's file, not the host")
		assert.Contains(t, err.Error(), "2", "the line they have to go fix")
		assertSaysNothingAboutTheNetwork(t, err)
	})

	t.Run("an empty file is refused on its own terms", func(t *testing.T) {
		err := createFrom(t, "\n   \n")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "rows.jsonl")
		assertSaysNothingAboutTheNetwork(t, err)
	})
}

// assertSaysNothingAboutTheNetwork fails if the message came from a request the
// command had no reason to make.
func assertSaysNothingAboutTheNetwork(t *testing.T, err error) {
	t.Helper()

	for _, leaked := range []string{
		"no such host", "dial tcp", "HTTP request failed",
		"dataset-tests.invalid", "already exists", "credential",
	} {
		assert.NotContains(t, err.Error(), leaked,
			"the file was unreadable before any of this was worth finding out")
	}
}

// createFrom runs `dataset create` against a file holding the given rows.
func createFrom(t *testing.T, rows string) error {
	t.Helper()

	path := filepath.Join(t.TempDir(), "rows.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(rows), 0o600))

	cmd := newDatasetWriteCommand("create", "Register a dataset.")
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	// Global flags azd would have supplied.
	cmd.Flags().Bool("no-prompt", false, "")
	cmd.Flags().String("output", "", "")
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ds", "--from-file", path})
	cmd.SetContext(t.Context())

	return cmd.Execute()
}
