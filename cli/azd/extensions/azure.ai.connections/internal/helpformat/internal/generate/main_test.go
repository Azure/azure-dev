// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore helpformat
package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateOnlyAllowedExtensions(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "azure.ai.connections", "internal", "helpformat")
	require.NoError(t, os.MkdirAll(sourceDir, 0750))
	source := []byte("// Copyright\n\n//go:generate go run ./internal/generate\n\npackage helpformat\n")
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "helpformat.go"), source, 0600))
	agentPath := filepath.Join(root, "azure.ai.agents", "internal", "helpformat", "helpformat.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(agentPath), 0750))
	require.NoError(t, os.WriteFile(agentPath, []byte("unchanged agent formatter"), 0600))
	t.Chdir(sourceDir)

	require.NoError(t, generate())

	var files []string
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(relative))
		}
		return nil
	}))
	require.ElementsMatch(t, []string{
		"azure.ai.agents/internal/helpformat/helpformat.go",
		"azure.ai.connections/internal/helpformat/helpformat.go",
		"azure.ai.projects/internal/helpformat/helpformat.go",
		"azure.ai.routines/internal/helpformat/helpformat.go",
		"azure.ai.skills/internal/helpformat/helpformat.go",
		"azure.ai.toolboxes/internal/helpformat/helpformat.go",
	}, files)
	// #nosec G304 -- Reads the fixture created in this test's temporary directory.
	agent, err := os.ReadFile(agentPath)
	require.NoError(t, err)
	require.Equal(t, "unchanged agent formatter", string(agent))
	unchangedSource, err := os.ReadFile("helpformat.go")
	require.NoError(t, err)
	require.Equal(t, source, unchangedSource)
}
