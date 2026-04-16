// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// GetProjectDir
// ---------------------------------------------------------------------------

func TestGetProjectDir_EnvVarOverride(t *testing.T) {
	expected := filepath.Join(t.TempDir(), "my-project")
	t.Setenv("AZD_EXEC_PROJECT_DIR", expected)

	dir, err := GetProjectDir()
	require.NoError(t, err)
	require.Equal(t, expected, dir)
}

func TestGetProjectDir_EnvVarEmptyFallsThrough(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")

	// With an empty env var the function walks up from cwd.
	// In the test environment there is no azure.yaml, so we expect an error.
	_, err := GetProjectDir()
	require.ErrorIs(t, err, ErrProjectNotFound)
}

// ---------------------------------------------------------------------------
// FindFileUpward
// ---------------------------------------------------------------------------

func TestFindFileUpward_FileInStartDir(t *testing.T) {
	root := t.TempDir()
	err := os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test"), 0600)
	require.NoError(t, err)

	dir, err := FindFileUpward(root, "azure.yaml")
	require.NoError(t, err)
	require.Equal(t, root, dir)
}

func TestFindFileUpward_FileInParentDir(t *testing.T) {
	root := t.TempDir()
	err := os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test"), 0600)
	require.NoError(t, err)

	child := filepath.Join(root, "src", "app")
	err = os.MkdirAll(child, 0700)
	require.NoError(t, err)

	dir, err := FindFileUpward(child, "azure.yaml")
	require.NoError(t, err)
	require.Equal(t, root, dir)
}

func TestFindFileUpward_FileInGrandparentDir(t *testing.T) {
	root := t.TempDir()
	err := os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test"), 0600)
	require.NoError(t, err)

	deep := filepath.Join(root, "a", "b", "c")
	err = os.MkdirAll(deep, 0700)
	require.NoError(t, err)

	dir, err := FindFileUpward(deep, "azure.yaml")
	require.NoError(t, err)
	require.Equal(t, root, dir)
}

func TestFindFileUpward_NotFound(t *testing.T) {
	// Use a temp dir that definitely has no azure.yaml in its ancestry.
	root := t.TempDir()
	child := filepath.Join(root, "empty")
	err := os.MkdirAll(child, 0700)
	require.NoError(t, err)

	_, err = FindFileUpward(child, "azure.yaml")
	require.ErrorIs(t, err, ErrProjectNotFound)
}

func TestFindFileUpward_DirectoryWithSameNameIsSkipped(t *testing.T) {
	root := t.TempDir()

	// Create a directory named "azure.yaml" (not a file).
	err := os.MkdirAll(filepath.Join(root, "azure.yaml"), 0700)
	require.NoError(t, err)

	_, err = FindFileUpward(root, "azure.yaml")
	require.ErrorIs(t, err, ErrProjectNotFound)
}
