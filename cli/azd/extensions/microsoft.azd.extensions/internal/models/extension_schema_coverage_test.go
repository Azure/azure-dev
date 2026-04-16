// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
)

func TestExtensionSchema_SafeDashId(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		expected string
	}{
		{
			name:     "DottedId",
			id:       "azure.ai.models",
			expected: "azure-ai-models",
		},
		{
			name:     "NoDots",
			id:       "simple",
			expected: "simple",
		},
		{
			name:     "SingleDot",
			id:       "azure.ai",
			expected: "azure-ai",
		},
		{
			name:     "AlreadyDashed",
			id:       "azure-ai-models",
			expected: "azure-ai-models",
		},
		{
			name:     "EmptyId",
			id:       "",
			expected: "",
		},
		{
			name:     "LeadingDot",
			id:       ".hidden",
			expected: "-hidden",
		},
		{
			name:     "TrailingDot",
			id:       "ext.",
			expected: "ext-",
		},
		{
			name:     "MultipleDots",
			id:       "a.b.c.d.e",
			expected: "a-b-c-d-e",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := &ExtensionSchema{Id: tt.id}
			require.Equal(t, tt.expected, schema.SafeDashId())
		})
	}
}

func TestLoadExtension_Success(t *testing.T) {
	tempDir := t.TempDir()
	yamlContent := `id: test.extension
version: "1.0.0"
displayName: Test Extension
description: A test extension
usage: test usage
capabilities:
  - custom-commands
`
	require.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "extension.yaml"),
		[]byte(yamlContent),
		0600,
	))

	ext, err := LoadExtension(tempDir)
	require.NoError(t, err)
	require.NotNil(t, ext)
	require.Equal(t, "test.extension", ext.Id)
	require.Equal(t, "1.0.0", ext.Version)
	require.Equal(t, "Test Extension", ext.DisplayName)
	require.Equal(t, "A test extension", ext.Description)
	require.Equal(t, "test usage", ext.Usage)
	require.Len(t, ext.Capabilities, 1)
	require.Equal(t,
		extensions.CustomCommandCapability, ext.Capabilities[0],
	)

	absPath, err := filepath.Abs(tempDir)
	require.NoError(t, err)
	require.Equal(t, absPath, ext.Path)
}

func TestLoadExtension_FileNotFound(t *testing.T) {
	tempDir := t.TempDir()

	ext, err := LoadExtension(tempDir)
	require.Error(t, err)
	require.Nil(t, ext)
	require.Contains(t, err.Error(), "Extension manifest file not found")
}

func TestLoadExtension_MissingId(t *testing.T) {
	tempDir := t.TempDir()
	yamlContent := `version: "1.0.0"
displayName: Test
description: desc
usage: usage
`
	require.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "extension.yaml"),
		[]byte(yamlContent),
		0600,
	))

	ext, err := LoadExtension(tempDir)
	require.Error(t, err)
	require.Nil(t, ext)
	require.Contains(t, err.Error(), "id is required")
}

func TestLoadExtension_MissingVersion(t *testing.T) {
	tempDir := t.TempDir()
	yamlContent := `id: test.extension
displayName: Test
description: desc
usage: usage
`
	require.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "extension.yaml"),
		[]byte(yamlContent),
		0600,
	))

	ext, err := LoadExtension(tempDir)
	require.Error(t, err)
	require.Nil(t, ext)
	require.Contains(t, err.Error(), "version is required")
}

func TestLoadExtension_InvalidYAML(t *testing.T) {
	tempDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "extension.yaml"),
		[]byte(":::invalid:::yaml:::"),
		0600,
	))

	ext, err := LoadExtension(tempDir)
	require.Error(t, err)
	require.Nil(t, ext)
	require.Contains(t, err.Error(), "id is required")
}

func TestLoadRegistry_Success(t *testing.T) {
	tempDir := t.TempDir()
	registry := extensions.Registry{
		Extensions: []*extensions.ExtensionMetadata{
			{
				Id:          "ext.one",
				DisplayName: "Extension One",
				Versions: []extensions.ExtensionVersion{
					{Version: "1.0.0"},
				},
			},
		},
	}

	data, err := json.MarshalIndent(registry, "", "  ")
	require.NoError(t, err)

	regPath := filepath.Join(tempDir, "registry.json")
	require.NoError(t, os.WriteFile(regPath, data, 0600))

	loaded, err := LoadRegistry(regPath)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Len(t, loaded.Extensions, 1)
	require.Equal(t, "ext.one", loaded.Extensions[0].Id)
	require.Equal(t, "Extension One", loaded.Extensions[0].DisplayName)
}

func TestLoadRegistry_FileNotFound(t *testing.T) {
	_, err := LoadRegistry("/nonexistent/registry.json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read registry file")
}

func TestLoadRegistry_InvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	regPath := filepath.Join(tempDir, "registry.json")
	require.NoError(t, os.WriteFile(
		regPath, []byte("not json"), 0600,
	))

	_, err := LoadRegistry(regPath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse registry file")
}

func TestLoadRegistry_EmptyRegistry(t *testing.T) {
	tempDir := t.TempDir()
	regPath := filepath.Join(tempDir, "registry.json")
	require.NoError(t, os.WriteFile(
		regPath, []byte(`{"extensions":[]}`), 0600,
	))

	loaded, err := LoadRegistry(regPath)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Empty(t, loaded.Extensions)
}
