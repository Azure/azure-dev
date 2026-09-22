// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestFindExistingAgentYaml(t *testing.T) {
	t.Parallel()

	t.Run("empty dir returns no match", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		got, err := findExistingAgentYaml(dir)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("finds agent.yaml", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeReuseTestFile(t, dir, "agent.yaml", "kind: hosted\nname: foo\n")

		got, err := findExistingAgentYaml(dir)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "agent.yaml"), got)
	})

	t.Run("agent.manifest.yaml takes priority over agent.yaml", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeReuseTestFile(t, dir, "agent.yaml", "kind: hosted\nname: foo\n")
		writeReuseTestFile(t, dir, "agent.manifest.yaml", "template:\n  kind: hosted\n  name: foo\n")

		got, err := findExistingAgentYaml(dir)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "agent.manifest.yaml"), got)
	})

	t.Run("directory entries are ignored", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "agent.yaml"), 0o750))

		got, err := findExistingAgentYaml(dir)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("scan does not recurse into subdirectories", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		nested := filepath.Join(dir, "src")
		require.NoError(t, os.Mkdir(nested, 0o750))
		writeReuseTestFile(t, nested, "agent.yaml", "kind: hosted\nname: foo\n")

		got, err := findExistingAgentYaml(dir)
		require.NoError(t, err)
		require.Empty(t, got, "shallow scan only; nested agent.yaml must be ignored")
	})

	t.Run("content is not parsed", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeReuseTestFile(t, dir, "agent.yaml", "name: : :\nmodel: [unterminated\n")

		got, err := findExistingAgentYaml(dir)
		require.NoError(t, err)
		require.Equal(t, path, got)
	})
}

func TestLegacyInitSourceError(t *testing.T) {
	t.Parallel()

	err := legacyInitSourceError(filepath.Join("project", "agent.manifest.yaml"))
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Message, "agent.manifest.yaml")
	require.Contains(t, localErr.Suggestion, "azure.yaml")
}

func writeReuseTestFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}
