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

// TestIsCwdEmptyForInit covers the empty/non-empty branch ensureProject
// uses to decide whether to scaffold the full starter template or just
// write a minimal azure.yaml inline.
func TestIsCwdEmptyForInit(t *testing.T) {
	t.Run("empty dir reports empty", func(t *testing.T) {
		dir := t.TempDir()
		empty, err := isCwdEmptyForInit(dir)
		require.NoError(t, err)
		assert.True(t, empty)
	})

	t.Run("dir with a regular file reports non-empty", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0o644)) //nolint:gosec
		empty, err := isCwdEmptyForInit(dir)
		require.NoError(t, err)
		assert.False(t, empty)
	})

	t.Run("dir with only an installed skill folder reports non-empty", func(t *testing.T) {
		// This is the case that broke after Round 2: pre-flow installs
		// `.agents/skills/azd-ai-skill/...`, then `azd ai agent init -m
		// <url> --no-prompt` runs in that dir. ensureProject must NOT
		// dispatch `azd init -t` here (the workflow auto-declines its
		// "directory not empty" prompt under --no-prompt and fails).
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".agents", "skills", "azd-ai-skill"), 0o755)) //nolint:gosec
		empty, err := isCwdEmptyForInit(dir)
		require.NoError(t, err)
		assert.False(t, empty,
			"a dir containing the installed AZD AI skill MUST report non-empty so "+
				"ensureProject takes the minimal-azure.yaml path instead of the "+
				"starter-template scaffold workflow")
	})
}

// TestWriteMinimalAzureYaml covers the contract ensureProject relies on
// when the cwd is non-empty: a 3-line azure.yaml exists afterwards with
// a derived name and the schema comment, and a pre-existing file is
// never clobbered.
func TestWriteMinimalAzureYaml(t *testing.T) {
	t.Run("writes a 3-line azure.yaml with derived name", func(t *testing.T) {
		dir := t.TempDir()
		// Name the dir something sanitizeAgentName accepts as-is so we
		// can assert on the substituted name without depending on the
		// full sanitization rules.
		projDir := filepath.Join(dir, "my-agent-proj")
		require.NoError(t, os.MkdirAll(projDir, 0o755)) //nolint:gosec

		require.NoError(t, writeMinimalAzureYaml(projDir))

		body, err := os.ReadFile(filepath.Join(projDir, "azure.yaml")) //nolint:gosec
		require.NoError(t, err)

		text := string(body)
		assert.Contains(t, text, "name: my-agent-proj",
			"name MUST be derived from the cwd basename so `azd` picks the right project name")
		assert.Contains(t, text, "yaml-language-server",
			"schema comment MUST be present so editors light up YAML completions")
		assert.NotContains(t, text, "services:",
			"minimal azure.yaml MUST NOT seed a services section -- addToProject does that later")
	})

	t.Run("never clobbers an existing azure.yaml", func(t *testing.T) {
		dir := t.TempDir()
		existing := "name: existing-project\nservices: {}\n"
		path := filepath.Join(dir, "azure.yaml")
		require.NoError(t, os.WriteFile(path, []byte(existing), 0o644)) //nolint:gosec

		require.NoError(t, writeMinimalAzureYaml(dir),
			"writeMinimalAzureYaml on a dir that already has an azure.yaml must be a safe no-op (the existing file is what `Project().Get()` should see)")

		body, err := os.ReadFile(path) //nolint:gosec
		require.NoError(t, err)
		assert.Equal(t, existing, string(body),
			"existing azure.yaml MUST be preserved byte-for-byte; clobbering it would lose the user's project config")
	})
}
