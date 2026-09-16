// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetAbsolutePath_ResolvesFromWorkingDirectory(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	relativePath := filepath.Join("registries", "registry.json")
	absolutePath := filepath.Join(workingDir, relativePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absolutePath), 0o755))
	require.NoError(t, os.WriteFile(absolutePath, []byte("{}"), 0o600))

	resolvedPath, err := getAbsolutePath(relativePath)

	require.NoError(t, err)
	require.Equal(t, absolutePath, resolvedPath)
}
