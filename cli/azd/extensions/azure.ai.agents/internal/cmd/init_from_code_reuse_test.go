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

// These tests cover the helpers that back the issue #7268 reuse path.
// runReuseDefinition itself is exercised by manual e2e (see
// cli/azd/docs/design/azure-ai-agent-init-reuse-agent-yaml.md §7) because a
// full unit test would require mocking the azd Project gRPC service in
// addition to the Environment/Prompt mocks that already exist; that
// investment is deferred until the next time a reuse-related change lands.

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

// TestRunReuseDefinition_InvalidFileReturnsStructuredError covers the
// "malformed template-wrapped YAML returns CodeInvalidAgentManifest" case the
// reviewer explicitly asked for. We invoke the failure path directly because
// it short-circuits before any azd gRPC calls are made and so does not need a
// full client mock.
func TestRunReuseDefinition_InvalidFileReturnsStructuredError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeReuseTestFile(t, dir, "agent.yaml", "name: : :\nmodel: [unterminated\n")

	err := runReuseDefinition(t.Context(), &initFlags{}, nil, nil, dir, path)
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.True(t, errors.As(err, &localErr), "expected *azdext.LocalError, got %T", err)
	require.Equal(t, exterrors.CodeInvalidAgentManifest, localErr.Code)
	require.NotEmpty(t, localErr.Suggestion)
	// Error message and suggestion should both reference the file by name.
	require.Contains(t, localErr.Message, "agent.yaml")
	require.Contains(t, localErr.Suggestion, "agent.yaml")
}

// TestRunReuseDefinition_RejectsManifestShapedFile covers the case where the
// scan picks up a file that looks like a manifest but failed upstream
// validation. The user sees a targeted error instead of falling through to
// scaffolding prompts.
func TestRunReuseDefinition_RejectsManifestShapedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Manifest-shaped but missing required fields, so detectLocalManifest would
	// have rejected it upstream and our reuse scan picks it up here.
	path := writeReuseTestFile(t, dir, "agent.manifest.yaml",
		"template:\n  # intentionally incomplete\n")

	err := runReuseDefinition(t.Context(), &initFlags{}, nil, nil, dir, path)
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.True(t, errors.As(err, &localErr))
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
