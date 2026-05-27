// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/exterrors"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// Helpers backing the issue #7268 reuse path. runReuseDefinition's happy path
// is covered by manual e2e; only its failure branches are unit-tested here
// because they short-circuit before any azd gRPC calls.

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
}

func TestLoadAgentDefinitionFile(t *testing.T) {
	t.Parallel()

	t.Run("happy path: bare definition with hosted kind", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeReuseTestFile(t, dir, "agent.yaml",
			"kind: hosted\nname: my-agent\nmodel:\n  id: gpt-4o-mini\n")

		def, err := loadAgentDefinitionFile(path)
		require.NoError(t, err)
		require.NotNil(t, def)
		require.Equal(t, "my-agent", def.Name)
		require.Nil(t, def.CodeConfiguration)
	})

	t.Run("happy path: code_configuration preserved", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeReuseTestFile(t, dir, "agent.yaml",
			"kind: hosted\nname: my-agent\ncode_configuration:\n  runtime: python_3_12\n  entry_point: main.py\n")

		def, err := loadAgentDefinitionFile(path)
		require.NoError(t, err)
		require.NotNil(t, def.CodeConfiguration)
		require.Equal(t, "python_3_12", def.CodeConfiguration.Runtime)
	})

	t.Run("rejects manifest-shaped file (top-level template key)", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeReuseTestFile(t, dir, "agent.yaml", "template:\n  kind: hosted\n  name: foo\n")

		_, err := loadAgentDefinitionFile(path)
		require.Error(t, err)
		require.Contains(t, err.Error(), "template")
	})

	t.Run("rejects missing kind via ValidateAgentDefinition", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeReuseTestFile(t, dir, "agent.yaml", "name: my-agent\n")

		_, err := loadAgentDefinitionFile(path)
		require.Error(t, err)
		require.Contains(t, err.Error(), "kind")
	})

	t.Run("rejects invalid agent name via ValidateAgentDefinition", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeReuseTestFile(t, dir, "agent.yaml", "kind: hosted\nname: \"!!! invalid\"\n")

		_, err := loadAgentDefinitionFile(path)
		require.Error(t, err)
	})

	t.Run("rejects broken yaml", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeReuseTestFile(t, dir, "agent.yaml", "name: : :\nmodel: [unterminated\n")

		_, err := loadAgentDefinitionFile(path)
		require.Error(t, err)
	})
}

// Malformed YAML must surface as CodeInvalidAgentManifest. Runs against the
// failure path so no gRPC mock is needed.
func TestRunReuseDefinition_InvalidFileReturnsStructuredError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeReuseTestFile(t, dir, "agent.yaml", "name: : :\nmodel: [unterminated\n")

	err := runReuseDefinition(t.Context(), &initFlags{}, nil, nil, dir, path)
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected *azdext.LocalError, got %T", err)
	require.Equal(t, exterrors.CodeInvalidAgentManifest, localErr.Code)
	require.NotEmpty(t, localErr.Suggestion)
	require.Contains(t, localErr.Message, "agent.yaml")
	require.Contains(t, localErr.Suggestion, "agent.yaml")
}

// A manifest-shaped file that failed upstream validation must produce a
// targeted error here, not fall into scaffolding.
func TestRunReuseDefinition_RejectsManifestShapedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeReuseTestFile(t, dir, "agent.manifest.yaml",
		"template:\n  # intentionally incomplete\n")

	err := runReuseDefinition(t.Context(), &initFlags{}, nil, nil, dir, path)
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, exterrors.CodeInvalidAgentManifest, localErr.Code)
	require.Contains(t, localErr.Message, "agent.manifest.yaml",
		"error message must name the actual file, not a hardcoded agent.yaml")
}

func writeReuseTestFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}
