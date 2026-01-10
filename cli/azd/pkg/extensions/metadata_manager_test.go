// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetadataManager_FetchAndCache_NoCapability(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	manager := NewMetadataManager(configManager)

	extension := &Extension{
		Id:           "test.extension",
		Version:      "1.0.0",
		Capabilities: []CapabilityType{CustomCommandCapability},
	}

	err := manager.FetchAndCache(context.Background(), extension)
	require.NoError(t, err, "Should not error when extension doesn't have metadata capability")
}

func TestMetadataManager_LoadAndDelete(t *testing.T) {
	// Create temp directory for test
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	manager := NewMetadataManager(configManager)

	extensionId := "test.extension"
	extensionDir := filepath.Join(tempDir, "extensions", extensionId)
	require.NoError(t, os.MkdirAll(extensionDir, 0755))

	// Create test metadata
	testMetadata := ExtensionCommandMetadata{
		SchemaVersion: "1.0",
		ID:            extensionId,
		Version:       "1.0.0",
		Commands: []Command{
			{
				Name:  []string{"test", "command"},
				Short: "Test command",
			},
		},
	}

	metadataJSON, err := json.MarshalIndent(testMetadata, "", "  ")
	require.NoError(t, err)

	metadataPath := filepath.Join(extensionDir, metadataFileName)
	require.NoError(t, os.WriteFile(metadataPath, metadataJSON, 0600))

	// Test Load
	loaded, err := manager.Load(extensionId)
	require.NoError(t, err)
	assert.Equal(t, testMetadata.SchemaVersion, loaded.SchemaVersion)
	assert.Equal(t, testMetadata.ID, loaded.ID)
	assert.Equal(t, testMetadata.Version, loaded.Version)
	assert.Len(t, loaded.Commands, 1)
	assert.Equal(t, []string{"test", "command"}, loaded.Commands[0].Name)

	// Test Exists
	assert.True(t, manager.Exists(extensionId))

	// Test Delete
	err = manager.Delete(extensionId)
	require.NoError(t, err)
	assert.False(t, manager.Exists(extensionId))

	// Test Delete when file doesn't exist (should not error)
	err = manager.Delete(extensionId)
	require.NoError(t, err)
}

func TestMetadataManager_Load_NotFound(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	manager := NewMetadataManager(configManager)

	_, err := manager.Load("nonexistent.extension")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata not found")
}

func TestMetadataManager_Load_InvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	manager := NewMetadataManager(configManager)

	extensionId := "test.extension"
	extensionDir := filepath.Join(tempDir, "extensions", extensionId)
	require.NoError(t, os.MkdirAll(extensionDir, 0755))

	metadataPath := filepath.Join(extensionDir, metadataFileName)
	require.NoError(t, os.WriteFile(metadataPath, []byte("invalid json"), 0600))

	_, err := manager.Load(extensionId)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse metadata JSON")
}

func TestMetadataManager_Exists(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	manager := NewMetadataManager(configManager)

	extensionId := "test.extension"

	// Should return false when metadata doesn't exist
	assert.False(t, manager.Exists(extensionId))

	// Create metadata file
	extensionDir := filepath.Join(tempDir, "extensions", extensionId)
	require.NoError(t, os.MkdirAll(extensionDir, 0755))
	metadataPath := filepath.Join(extensionDir, metadataFileName)
	require.NoError(t, os.WriteFile(metadataPath, []byte("{}"), 0600))

	// Should return true now
	assert.True(t, manager.Exists(extensionId))
}
