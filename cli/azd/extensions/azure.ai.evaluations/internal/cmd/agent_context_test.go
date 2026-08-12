// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The generation spec names an instructions file relative to itself, not to the
// working directory, so `generate --config <elsewhere>` reads the same file the
// author sees next to the spec.
func TestDeclaredInstructions_ResolvesRelativeToTheSpec(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "evals")
	require.NoError(t, os.MkdirAll(filepath.Join(specDir, "agent"), 0o750))

	body := "Answer only from the product catalog."
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "agent", "instructions.md"), []byte("  "+body+"\n"), 0o600))

	got, err := declaredInstructions(
		"./agent/instructions.md", filepath.Join(specDir, "generate.yaml"))
	require.NoError(t, err)
	assert.Equal(t, body, got, "the file's contents should be used, trimmed")
}

// A path can be declared before that file exists. Treating the gap as an error
// would break the flow `init` itself scaffolds.
func TestDeclaredInstructions_MissingFileIsNotAnError(t *testing.T) {
	got, err := declaredInstructions(
		"./agent/instructions.md", filepath.Join(t.TempDir(), "generate.yaml"))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestDeclaredInstructions_UnsetIsEmpty(t *testing.T) {
	got, err := declaredInstructions("", "generate.yaml")
	require.NoError(t, err)
	assert.Empty(t, got)
}

// Only the newest version is read, and an agent with no published version must
// not panic the caller.
func TestAgentInstructions(t *testing.T) {
	var agent eval_api.Agent
	require.NoError(t, json.Unmarshal([]byte(`{
      "name": "support",
      "versions": { "latest": { "version": "2", "definition": {
        "model": "gpt-5-mini",
        "instructions": "  You are a support assistant.\n" } } }
    }`), &agent))
	assert.Equal(t, "You are a support assistant.", agent.Instructions())

	var empty eval_api.Agent
	require.NoError(t, json.Unmarshal([]byte(`{"name":"x","versions":{}}`), &empty))
	assert.Empty(t, empty.Instructions(), "an agent with no published version has no instructions")

	var nilAgent *eval_api.Agent
	assert.Empty(t, nilAgent.Instructions())
}
