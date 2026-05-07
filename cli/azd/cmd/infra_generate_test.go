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

func TestDetermineDuplicates(t *testing.T) {
	t.Parallel()

	t.Run("no_duplicates", func(t *testing.T) {
		t.Parallel()

		source := t.TempDir()
		target := t.TempDir()

		// Create files only in source
		require.NoError(t, os.WriteFile(filepath.Join(source, "main.bicep"), []byte("source"), 0600))

		duplicates, err := determineDuplicates(source, target)
		require.NoError(t, err)
		assert.Empty(t, duplicates)
	})

	t.Run("with_duplicates", func(t *testing.T) {
		t.Parallel()

		source := t.TempDir()
		target := t.TempDir()

		// Create same file in both
		require.NoError(t, os.WriteFile(filepath.Join(source, "main.bicep"), []byte("source"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(target, "main.bicep"), []byte("target"), 0600))

		duplicates, err := determineDuplicates(source, target)
		require.NoError(t, err)
		assert.Equal(t, []string{"main.bicep"}, duplicates)
	})

	t.Run("nested_duplicates", func(t *testing.T) {
		t.Parallel()

		source := t.TempDir()
		target := t.TempDir()

		// Create nested directory structure
		require.NoError(t, os.MkdirAll(filepath.Join(source, "modules"), 0700))
		require.NoError(t, os.MkdirAll(filepath.Join(target, "modules"), 0700))

		require.NoError(t, os.WriteFile(
			filepath.Join(source, "modules", "storage.bicep"), []byte("s"), 0600))
		require.NoError(t, os.WriteFile(
			filepath.Join(target, "modules", "storage.bicep"), []byte("t"), 0600))

		// Also create a non-duplicate
		require.NoError(t, os.WriteFile(
			filepath.Join(source, "main.bicep"), []byte("s"), 0600))

		duplicates, err := determineDuplicates(source, target)
		require.NoError(t, err)
		assert.Len(t, duplicates, 1)
		assert.Contains(t, duplicates[0], "storage.bicep")
	})

	t.Run("empty_source", func(t *testing.T) {
		t.Parallel()

		source := t.TempDir()
		target := t.TempDir()

		duplicates, err := determineDuplicates(source, target)
		require.NoError(t, err)
		assert.Empty(t, duplicates)
	})
}
