// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestResolveProjectName(t *testing.T) {
	t.Run("uses azure yaml name", func(t *testing.T) {
		projectDir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(projectDir, "azure.yaml"),
			[]byte("name: configured-project\n"),
			0o600,
		))

		projectName, err := resolveProjectName(projectDir)

		require.NoError(t, err)
		require.Equal(t, "configured-project", projectName)
	})

	t.Run("uses project directory when name is empty", func(t *testing.T) {
		parentDir := t.TempDir()
		projectDir := filepath.Join(parentDir, "folder-project")
		require.NoError(t, os.Mkdir(projectDir, 0o700))
		require.NoError(t, os.WriteFile(
			filepath.Join(projectDir, "azure.yaml"),
			[]byte("services: {}\n"),
			0o600,
		))

		projectName, err := resolveProjectName(projectDir)

		require.NoError(t, err)
		require.Equal(t, "folder-project", projectName)
	})

	t.Run("uses project directory when azure yaml is absent", func(t *testing.T) {
		parentDir := t.TempDir()
		projectDir := filepath.Join(parentDir, "working-folder")
		require.NoError(t, os.Mkdir(projectDir, 0o700))

		projectName, err := resolveProjectName(projectDir)

		require.NoError(t, err)
		require.Equal(t, "working-folder", projectName)
	})

	t.Run("prefers azure yaml over azure yml", func(t *testing.T) {
		projectDir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(projectDir, "azure.yaml"),
			[]byte("name: yaml-project\n"),
			0o600,
		))
		require.NoError(t, os.WriteFile(
			filepath.Join(projectDir, "azure.yml"),
			[]byte("name: yml-project\n"),
			0o600,
		))

		projectName, err := resolveProjectName(projectDir)

		require.NoError(t, err)
		require.Equal(t, "yaml-project", projectName)
	})

	t.Run("preserves project name within ARM tag limit", func(t *testing.T) {
		projectName := strings.Repeat("a", maxProjectTagValueLength)

		require.Equal(t, projectName, normalizeProjectName(projectName))
	})

	t.Run("shortens long project name deterministically", func(t *testing.T) {
		projectName := strings.Repeat("a", maxProjectTagValueLength+1)

		normalized := normalizeProjectName(projectName)

		require.Len(t, []rune(normalized), maxProjectTagValueLength)
		require.Equal(t, normalized, normalizeProjectName(projectName))
		require.NotEqual(t, normalized, normalizeProjectName(projectName+"b"))
	})

	t.Run("does not split unicode project name", func(t *testing.T) {
		projectName := strings.Repeat("界", maxProjectTagValueLength+1)

		normalized := normalizeProjectName(projectName)

		require.Len(t, []rune(normalized), maxProjectTagValueLength)
		require.True(t, utf8.ValidString(normalized))
	})
}
