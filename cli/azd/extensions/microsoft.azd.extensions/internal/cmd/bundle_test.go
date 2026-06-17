// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/stretchr/testify/require"
)

func TestResolveBundleOutputPath(t *testing.T) {
	t.Parallel()

	ext := &models.ExtensionSchema{Id: "microsoft.test", Version: "1.2.3"}
	expectedName := "microsoft-test_1.2.3.zip"

	t.Run("empty uses cwd", func(t *testing.T) {
		cwd, err := os.Getwd()
		require.NoError(t, err)
		got, err := resolveBundleOutputPath("", ext)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(cwd, expectedName), got)
	})

	t.Run("directory appends derived name", func(t *testing.T) {
		dir := t.TempDir()
		got, err := resolveBundleOutputPath(dir, ext)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, expectedName), got)
	})

	t.Run("explicit zip used verbatim", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "custom.zip")
		got, err := resolveBundleOutputPath(target, ext)
		require.NoError(t, err)
		require.Equal(t, target, got)
	})
}

func TestZipDirectory_PreservesStructure(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "registry.json"), []byte("{}"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(sourceDir, "artifacts"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "artifacts", "ext.zip"), []byte("bin"), 0600))

	target := filepath.Join(t.TempDir(), "bundle.zip")
	require.NoError(t, zipDirectory(sourceDir, target))

	reader, err := zip.OpenReader(target)
	require.NoError(t, err)
	defer reader.Close()

	names := map[string]bool{}
	for _, f := range reader.File {
		names[f.Name] = true
	}

	// Nested directory structure is preserved with forward-slash paths.
	require.True(t, names["registry.json"])
	require.True(t, names["artifacts/ext.zip"])
}

func TestZipDirectory_OverwriteIsAtomic(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "registry.json"), []byte("{}"), 0600))

	outDir := t.TempDir()
	target := filepath.Join(outDir, "bundle.zip")

	// A pre-existing file at the target is replaced cleanly.
	require.NoError(t, os.WriteFile(target, []byte("stale"), 0600))
	require.NoError(t, zipDirectory(sourceDir, target))

	// Re-packing over the existing bundle succeeds.
	require.NoError(t, zipDirectory(sourceDir, target))

	// The result is a valid, readable zip.
	reader, err := zip.OpenReader(target)
	require.NoError(t, err)
	defer reader.Close()
	require.NotEmpty(t, reader.File)

	// No temporary files are left behind in the output directory.
	entries, err := os.ReadDir(outDir)
	require.NoError(t, err)
	for _, e := range entries {
		require.Equal(t, "bundle.zip", e.Name(), "unexpected leftover file %q", e.Name())
	}
}
