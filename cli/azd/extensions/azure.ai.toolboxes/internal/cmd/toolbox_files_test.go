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

// `tools` on the create shape is accepted as a raw OpenAI.Tool[] pass-through.
// YAML must decode nested objects as map[string]any (not map[any]any) so the
// JSON marshaller in the data-plane client emits valid JSON.
func TestParseToolboxFile_AcceptsRawToolsOnCreate(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		path := writeTempFile(t, ".json", `
{
  "tools": [
    { "type": "web_search",  "name": "web" },
    { "type": "file_search", "name": "files" }
  ]
}`)
		var out toolboxCreateFile
		require.NoError(t, parseToolboxFile(path, &out))
		require.Len(t, out.Tools, 2)
		assert.Equal(t, "web_search", out.Tools[0]["type"])
		assert.Equal(t, "files", out.Tools[1]["name"])
	})

	t.Run("yaml decodes nested maps as map[string]any", func(t *testing.T) {
		path := writeTempFile(t, ".yaml", `
tools:
  - type: web_search
    name: web
  - type: code_interpreter
    name: ci
    container:
      type: auto
`)
		var out toolboxCreateFile
		require.NoError(t, parseToolboxFile(path, &out))
		require.Len(t, out.Tools, 2)
		nested, ok := out.Tools[1]["container"].(map[string]any)
		require.True(t, ok, "expected nested map[string]any, got %T", out.Tools[1]["container"])
		assert.Equal(t, "auto", nested["type"])
	})
}

func TestParseToolboxFile_RejectsMissingFile(t *testing.T) {
	var out toolboxCreateFile
	err := parseToolboxFile(filepath.Join(t.TempDir(), "nope.json"), &out)
	requireLocalError(t, err, exterrors.CodeInvalidParameter)
}

// `policies.raiConfig` is accepted by `toolbox create` and maps to the
// service-owned wire policy shape later in request construction.
func TestParseToolboxFile_AcceptsPolicies(t *testing.T) {
	t.Run("yaml with raiPolicyName", func(t *testing.T) {
		path := writeTempFile(t, ".yaml", `
description: with policy
connections:
  - name: my-mcp
policies:
  raiConfig:
    raiPolicyName: Microsoft.Default
`)
		var out toolboxCreateFile
		require.NoError(t, parseToolboxFile(path, &out))
		require.NotNil(t, out.Policies)
		require.NotNil(t, out.Policies.RaiConfig)
		assert.Equal(t, "Microsoft.Default", out.Policies.RaiConfig.RaiPolicyName)
		assert.Equal(t, "Microsoft.Default", out.Policies.RaiConfig.ResolvedPolicyName())
	})

	// The friendlier `name` alias also resolves, but is not the wire field.
	t.Run("yaml with name alias", func(t *testing.T) {
		path := writeTempFile(t, ".yaml", `
description: with policy
connections:
  - name: my-mcp
policies:
  raiConfig:
    name: Microsoft.Default
`)
		var out toolboxCreateFile
		require.NoError(t, parseToolboxFile(path, &out))
		require.NotNil(t, out.Policies)
		require.NotNil(t, out.Policies.RaiConfig)
		assert.Equal(t, "", out.Policies.RaiConfig.RaiPolicyName)
		assert.Equal(t, "Microsoft.Default", out.Policies.RaiConfig.Name)
		assert.Equal(t, "Microsoft.Default", out.Policies.RaiConfig.ResolvedPolicyName())
	})

	// raiPolicyName wins over name when both are set.
	t.Run("raiPolicyName wins over name alias", func(t *testing.T) {
		spec := &toolboxRaiConfigSpec{RaiPolicyName: "wire", Name: "alias"}
		assert.Equal(t, "wire", spec.ResolvedPolicyName())
	})

	// Empty/whitespace-only fields return "" so the create flow can reject.
	t.Run("empty resolves to empty string", func(t *testing.T) {
		spec := &toolboxRaiConfigSpec{RaiPolicyName: "  "}
		assert.Equal(t, "", spec.ResolvedPolicyName())
	})
}

func TestParseToolboxFile_RejectsLegacySnakeCaseFields(t *testing.T) {
	tests := []struct {
		name        string
		ext         string
		content     string
		legacyKey   string
		replacement string
	}{
		{
			name:        "YAML connection instance",
			ext:         ".yaml",
			content:     "connections:\n  - name: search\n    instance_name: docs-config\n",
			legacyKey:   "instance_name",
			replacement: "instanceName",
		},
		{
			name:        "JSON connection instance",
			ext:         ".json",
			content:     `{"connections":[{"name":"search","instance_name":"docs-config"}]}`,
			legacyKey:   "instance_name",
			replacement: "instanceName",
		},
		{
			name:        "YAML RAI config",
			ext:         ".yaml",
			content:     "policies:\n  rai_config:\n    raiPolicyName: default\n",
			legacyKey:   "rai_config",
			replacement: "raiConfig",
		},
		{
			name:        "JSON RAI config",
			ext:         ".json",
			content:     `{"policies":{"rai_config":{"raiPolicyName":"default"}}}`,
			legacyKey:   "rai_config",
			replacement: "raiConfig",
		},
		{
			name:        "YAML RAI policy name",
			ext:         ".yaml",
			content:     "policies:\n  raiConfig:\n    rai_policy_name: default\n",
			legacyKey:   "rai_policy_name",
			replacement: "raiPolicyName",
		},
		{
			name:        "JSON RAI policy name",
			ext:         ".json",
			content:     `{"policies":{"raiConfig":{"rai_policy_name":"default"}}}`,
			legacyKey:   "rai_policy_name",
			replacement: "raiPolicyName",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeTempFile(t, test.ext, test.content)
			var out toolboxCreateFile

			err := parseToolboxFile(path, &out)
			localErr := requireLocalError(t, err, exterrors.CodeInvalidParameter)
			assert.Contains(t, localErr.Message, test.legacyKey)
			assert.Contains(t, localErr.Suggestion, test.replacement)
		})
	}
}
