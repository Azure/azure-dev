// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func getText(result *mcp.CallToolResult) string {
	if len(result.Content) > 0 {
		if txt, ok := result.Content[0].(mcp.TextContent); ok {
			return txt.Text
		}
	}
	return ""
}

func TestHandleAzdYamlSchema_ValidYaml(t *testing.T) {
	t.Parallel()
	// Arrange
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "azure.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("name: testapp\n"), 0600))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": yamlPath}

	// Act
	result, err := HandleAzdYamlSchema(context.Background(), req)

	// Assert
	require.NoError(t, err)
	text := getText(result)
	require.Contains(t, text, "azure.yaml is valid against the stable schema.")
}

func TestHandleAzdYamlSchema_MissingYaml(t *testing.T) {
	t.Parallel()
	// Arrange
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "azure.yaml")

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": yamlPath}

	// Act
	result, err := HandleAzdYamlSchema(context.Background(), req)

	// Assert
	require.NoError(t, err)
	text := getText(result)
	require.Contains(t, text, "azure.yaml not found")
}

func TestHandleAzdYamlSchema_InvalidYaml(t *testing.T) {
	t.Parallel()
	// Arrange
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "azure.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("name: !@#$\n"), 0600))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": yamlPath}

	// Act
	result, err := HandleAzdYamlSchema(context.Background(), req)

	// Assert
	require.NoError(t, err)
	text := getText(result)
	require.Contains(t, text, "Failed to unmarshal azure.yaml")
}

func TestHandleAzdYamlSchema_YamlNotValidSyntax(t *testing.T) {
	t.Parallel()
	// Arrange
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "azure.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("name: !@#$\n:bad"), 0600)) // not valid YAML syntax

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": yamlPath}

	// Act
	result, err := HandleAzdYamlSchema(context.Background(), req)

	// Assert
	require.NoError(t, err)
	text := getText(result)
	require.Contains(t, text, "Failed to unmarshal azure.yaml")
}

func TestHandleAzdYamlSchema_YamlValidButSchemaInvalid(t *testing.T) {
	t.Parallel()
	// Arrange
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "azure.yaml")
	// Valid YAML syntax, but missing required 'name' field for schema validation
	require.NoError(t, os.WriteFile(yamlPath, []byte("some_field: value\n"), 0600))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": yamlPath}

	// Act
	result, err := HandleAzdYamlSchema(context.Background(), req)

	// Assert
	require.NoError(t, err)
	text := getText(result)
	require.Contains(t, text, "missing property 'name'")
}

func TestHandleAzdYamlSchema_InvalidYaml_Structural(t *testing.T) {
	t.Parallel()
	// Arrange
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "azure.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("name: 123\n"), 0600)) // valid YAML, but not valid type for schema

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": yamlPath}

	// Act
	result, err := HandleAzdYamlSchema(context.Background(), req)

	// Assert
	require.NoError(t, err)
	text := getText(result)
	require.Contains(t, text, "jsonschema validation failed")
}
