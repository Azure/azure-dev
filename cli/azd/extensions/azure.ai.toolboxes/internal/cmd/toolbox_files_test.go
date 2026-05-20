// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.toolboxes/internal/exterrors"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTempFile writes content to <t.TempDir>/input<ext> and returns the path.
func writeTempFile(t *testing.T, ext, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "input"+ext)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestParseToolboxFile_AcceptsCreateShape(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		path := writeTempFile(t, ".json", `
{
  "description": "Sample toolbox",
  "connections": [
    { "name": "my-mcp" },
    { "name": "my-search", "index": "docs" }
  ]
}`)
		var out toolboxCreateFile
		require.NoError(t, parseToolboxFile(path, &out))
		assert.Equal(t, "Sample toolbox", out.Description)
		require.Len(t, out.Connections, 2)
		assert.Equal(t, "my-mcp", out.Connections[0].Name)
		assert.Equal(t, "docs", out.Connections[1].Index)
	})

	t.Run("yaml", func(t *testing.T) {
		path := writeTempFile(t, ".yaml", `
description: Sample toolbox
connections:
  - name: my-mcp
  - name: my-search
    index: docs
`)
		var out toolboxCreateFile
		require.NoError(t, parseToolboxFile(path, &out))
		assert.Equal(t, "Sample toolbox", out.Description)
		require.Len(t, out.Connections, 2)
	})
}

func TestParseToolboxFile_AcceptsAddShape(t *testing.T) {
	path := writeTempFile(t, ".json", `
{
  "connections": [
    { "name": "my-mcp" }
  ]
}`)
	var out toolboxToolsFile
	require.NoError(t, parseToolboxFile(path, &out))
	require.Len(t, out.Connections, 1)
	assert.Equal(t, "my-mcp", out.Connections[0].Name)
}

// `description` is `create`-only; if a user puts it in a `connection add`
// file, parsing must reject with a clear suggestion explaining that
// description is set at create time.
func TestParseToolboxFile_AddRejectsDescription(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		path := writeTempFile(t, ".json", `
{
  "description": "should be rejected here",
  "connections": [{ "name": "my-mcp" }]
}`)
		var out toolboxToolsFile
		err := parseToolboxFile(path, &out)
		le := requireLocalError(t, err, exterrors.CodeInvalidParameter)
		assert.Contains(t, le.Message, "description")
		assert.Contains(t, le.Suggestion, "toolbox create")
	})

	t.Run("yaml", func(t *testing.T) {
		path := writeTempFile(t, ".yaml", `
description: should be rejected here
connections:
  - name: my-mcp
`)
		var out toolboxToolsFile
		err := parseToolboxFile(path, &out)
		le := requireLocalError(t, err, exterrors.CodeInvalidParameter)
		assert.Contains(t, strings.ToLower(le.Message), "description")
		assert.Contains(t, le.Suggestion, "toolbox create")
	})
}

// Any other unknown key (typo on `connections`, stray `tools`, etc.) is
// rejected with the generic suggestion pointing at --help.
func TestParseToolboxFile_RejectsOtherUnknownFields(t *testing.T) {
	t.Run("typo on connections in create file", func(t *testing.T) {
		path := writeTempFile(t, ".json", `
{
  "description": "x",
  "conections": [{ "name": "my-mcp" }]
}`)
		var out toolboxCreateFile
		err := parseToolboxFile(path, &out)
		le := requireLocalError(t, err, exterrors.CodeInvalidParameter)
		assert.Contains(t, le.Suggestion, "--help")
	})

	t.Run("stray tools in add file", func(t *testing.T) {
		path := writeTempFile(t, ".json", `
{
  "connections": [{ "name": "my-mcp" }],
  "tools": [{ "type": "web_search", "name": "web" }]
}`)
		var out toolboxToolsFile
		err := parseToolboxFile(path, &out)
		le := requireLocalError(t, err, exterrors.CodeInvalidParameter)
		assert.Contains(t, le.Suggestion, "--help")
	})
}

func TestParseToolboxFile_RejectsUnsupportedExtension(t *testing.T) {
	path := writeTempFile(t, ".toml", `description = "x"`)
	var out toolboxCreateFile
	err := parseToolboxFile(path, &out)
	le := requireLocalError(t, err, exterrors.CodeInvalidParameter)
	assert.Contains(t, le.Suggestion, ".json")
}

func TestParseToolboxFile_RejectsMissingFile(t *testing.T) {
	var out toolboxCreateFile
	err := parseToolboxFile(filepath.Join(t.TempDir(), "nope.json"), &out)
	requireLocalError(t, err, exterrors.CodeInvalidParameter)
}
