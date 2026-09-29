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

// The list commands are backed by two different services whose envelopes
// disagree — `data` on one side, `value` on the other. Emitting whichever one
// came back would make a caller's parsing depend on that accident, so every
// list emits a bare array instead.
func TestEmitJSONList_EmitsAnArrayNotAnEnvelope(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, emitJSONList(&buf, []string{"a", "b"}))
	assert.Equal(t, "[\n  \"a\",\n  \"b\"\n]\n", buf.String())
}

// A nil slice marshals to `null`, which a caller iterating the output cannot
// range over. An empty listing has to come back as an empty array.
func TestEmitJSONList_NilBecomesEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	var none []string
	require.NoError(t, emitJSONList(&buf, none))
	assert.Equal(t, "[]\n", buf.String())
}

// `evaluator show --output-file` is pointed at a definition the developer is
// still working with, so a write that cannot complete must leave the old one
// intact rather than truncate it.
func TestWriteFileAtomic(t *testing.T) {
	t.Run("replaces an existing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "evaluator.json")
		require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))
		require.NoError(t, writeFileAtomic(path, []byte("new")))

		body, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "new", string(body))
	})

	t.Run("creates a file that was not there", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "evaluator.json")
		require.NoError(t, writeFileAtomic(path, []byte("new")))

		body, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "new", string(body))
	})

	t.Run("a directory is refused, not removed", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "evaluator.json")
		require.NoError(t, os.Mkdir(path, 0o750))

		require.Error(t, writeFileAtomic(path, []byte("new")))

		info, err := os.Stat(path)
		require.NoError(t, err, "the directory must survive")
		assert.True(t, info.IsDir())

		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, entries, 1, "no temporary file may be left behind")
	})
}
