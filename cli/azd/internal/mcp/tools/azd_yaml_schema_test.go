// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"os"
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
	// Arrange
	tmpDir := t.TempDir()
	tmpFile, err := os.CreateTemp(tmpDir, "azure.yaml")
	require.NoError(t, err)

	validYaml := []byte("name: testapp\n")
	_, err = tmpFile.Write(validYaml)
	require.NoError(t, err)
	tmpFile.Close()
	yamlPath := tmpFile.Name()

	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	os.Rename(yamlPath, "azure.yaml")
	defer os.Remove("azure.yaml")

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": "azure.yaml"}

	// Act
	result, err := HandleAzdYamlSchema(context.Background(), req)

	// Assert
	require.NoError(t, err)
	text := getText(result)
	require.Contains(t, text, "azure.yaml is valid against the stable schema.")
}

func TestHandleAzdYamlSchema_MissingYaml(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": "azure.yaml"}

	// Act
	result, err := HandleAzdYamlSchema(context.Background(), req)

	// Assert
	require.NoError(t, err)
	text := getText(result)
	require.Contains(t, text, "azure.yaml not found")
}

func TestHandleAzdYamlSchema_InvalidYaml(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	tmpFile, err := os.CreateTemp(tmpDir, "azure.yaml")
	require.NoError(t, err)

	invalidYaml := []byte("name: !@#$\n")
	_, err = tmpFile.Write(invalidYaml)
	require.NoError(t, err)
	tmpFile.Close()
	yamlPath := tmpFile.Name()

	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	os.Rename(yamlPath, "azure.yaml")
	defer os.Remove("azure.yaml")

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": "azure.yaml"}

	// Act
	result, err := HandleAzdYamlSchema(context.Background(), req)

	// Assert
	require.NoError(t, err)
	text := getText(result)
	require.Contains(t, text, "Failed to unmarshal azure.yaml")
}

func TestHandleAzdYamlSchema_YamlNotValidSyntax(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	tmpFile, err := os.CreateTemp(tmpDir, "azure.yaml")
	require.NoError(t, err)

	invalidYaml := []byte("name: !@#$\n:bad") // not valid YAML syntax
	_, err = tmpFile.Write(invalidYaml)
	require.NoError(t, err)
	tmpFile.Close()
	yamlPath := tmpFile.Name()

	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	os.Rename(yamlPath, "azure.yaml")
	defer os.Remove("azure.yaml")

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": "azure.yaml"}

	// Act
	result, err := HandleAzdYamlSchema(context.Background(), req)

	// Assert
	require.NoError(t, err)
	text := getText(result)
	require.Contains(t, text, "Failed to unmarshal azure.yaml")
}

func TestHandleAzdYamlSchema_YamlValidButSchemaInvalid(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	tmpFile, err := os.CreateTemp(tmpDir, "azure.yaml")
	require.NoError(t, err)

	// Valid YAML syntax, but missing required 'name' field for schema validation
	invalidSchemaYaml := []byte("some_field: value\n")
	_, err = tmpFile.Write(invalidSchemaYaml)
	require.NoError(t, err)
	tmpFile.Close()
	yamlPath := tmpFile.Name()

	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	os.Rename(yamlPath, "azure.yaml")
	defer os.Remove("azure.yaml")

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": "azure.yaml"}

	// Act
	result, err := HandleAzdYamlSchema(context.Background(), req)

	// Assert
	require.NoError(t, err)
	text := getText(result)
	require.Contains(t, text, "missing property 'name'")
}

func TestHandleAzdYamlSchema_InvalidYaml_Structural(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	tmpFile, err := os.CreateTemp(tmpDir, "azure.yaml")
	require.NoError(t, err)

	invalidYaml := []byte("name: 123\n") // valid YAML, but not valid type for schema
	_, err = tmpFile.Write(invalidYaml)
	require.NoError(t, err)
	tmpFile.Close()
	yamlPath := tmpFile.Name()

	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	os.Rename(yamlPath, "azure.yaml")
	defer os.Remove("azure.yaml")

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"path": "azure.yaml"}

	// Act
	result, err := HandleAzdYamlSchema(context.Background(), req)

	// Assert
	require.NoError(t, err)
	text := getText(result)
	require.Contains(t, text, "jsonschema validation failed")
}
