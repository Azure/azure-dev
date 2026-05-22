// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/ignore"
)

func TestShouldIgnoreWatchEventUsesRelativePathForHardcodedIgnores(t *testing.T) {
	root := t.TempDir()
	binPath := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(binPath, 0755))

	ignoreMatcher, err := ignore.NewMatcher(root)
	require.NoError(t, err)

	globIgnorePaths := []string{"bin", "bin/**/*"}

	require.True(t, shouldIgnoreWatchEvent(root, binPath, globIgnorePaths, ignoreMatcher))
	require.True(t, shouldIgnoreWatchEvent(root, filepath.Join(binPath, "extension.exe"), globIgnorePaths, ignoreMatcher))
	require.True(t, shouldIgnoreWatchEvent(root, "bin", globIgnorePaths, ignoreMatcher))
}

func TestShouldIgnoreWatchEventUsesRelativePathForIgnoreMatcher(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ignore.AzdxIgnoreFile), []byte("dist/\n"), 0600))

	distPath := filepath.Join(root, "dist")
	srcPath := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(distPath, 0755))
	require.NoError(t, os.MkdirAll(srcPath, 0755))

	ignoreMatcher, err := ignore.NewMatcher(root)
	require.NoError(t, err)

	require.True(t, shouldIgnoreWatchEvent(root, distPath, nil, ignoreMatcher))
	require.False(t, shouldIgnoreWatchEvent(root, srcPath, nil, ignoreMatcher))
}
