// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func Test_FileConfigManager_SaveAndLoadConfig(t *testing.T) {
	var azdConfig Config = NewConfig(
		map[string]any{
			"defaults": map[string]any{
				"location":     "eastus2",
				"subscription": "SUBSCRIPTION_ID",
			},
		},
	)

	configFilePath := filepath.Join(t.TempDir(), "config.json")
	configManager := NewFileConfigManager(NewManager())

	err := configManager.Save(azdConfig, configFilePath)
	require.NoError(t, err)

	existingConfig, err := configManager.Load(configFilePath)
	require.NoError(t, err)
	require.NotNil(t, existingConfig)
	require.Equal(t, azdConfig, existingConfig)
}

func Test_FileConfigManager_SaveAndLoadEmptyConfig(t *testing.T) {
	configFilePath := filepath.Join(t.TempDir(), "config.json")

	configManager := NewFileConfigManager(NewManager())
	azdConfig := NewConfig(nil)
	err := configManager.Save(azdConfig, configFilePath)
	require.NoError(t, err)

	existingConfig, err := configManager.Load(configFilePath)
	require.NoError(t, err)
	require.NotNil(t, existingConfig)
}

func Test_FileConfigManager_GetSetSecrets(t *testing.T) {
	tempDir := t.TempDir()
	azdConfigDir := filepath.Join(tempDir, ".azd")

	t.Setenv("AZD_CONFIG_DIR", azdConfigDir)

	// Set and save secrets
	configFilePath := filepath.Join(tempDir, "config.json")
	configManager := NewFileConfigManager(NewManager())
	azdConfig := NewConfig(nil)

	// Standard secrets
	expectedPassword := "P@55w0rd!"
	err := azdConfig.SetSecret("secrets.password", expectedPassword)
	require.NoError(t, err)

	err = azdConfig.SetSecret("infra.provisioning.sqlPassword", expectedPassword)
	require.NoError(t, err)

	// Missing vault reference
	missingVaultRef := fmt.Sprintf("vault://%s/%s", uuid.New().String(), uuid.New().String())
	err = azdConfig.Set("secrets.misingVaultRef", missingVaultRef)
	require.NoError(t, err)

	err = configManager.Save(azdConfig, configFilePath)
	require.NoError(t, err)

	baseConfig, ok := azdConfig.(*config)
	require.True(t, ok)

	expectedVaultPath := filepath.Join(azdConfigDir, "vaults", fmt.Sprintf("%s.json", baseConfig.vaultId))
	require.FileExists(t, expectedVaultPath)

	// Load and retrieve secrets
	updatedConfig, err := configManager.Load(configFilePath)
	require.NoError(t, err)
	require.NotNil(t, updatedConfig)

	userPassword, ok := updatedConfig.GetString("secrets.password")
	require.True(t, ok)
	require.Equal(t, expectedPassword, userPassword)

	sqlPassword, ok := updatedConfig.GetString("infra.provisioning.sqlPassword")
	require.True(t, ok)
	require.Equal(t, expectedPassword, sqlPassword)

	// Missing vault reference will return empty string
	// even though the value appears to be a vault reference
	missingPassword, ok := updatedConfig.GetString("secrets.misingVaultRef")
	require.False(t, ok)
	require.Empty(t, missingPassword)

	require.NotEqual(t, updatedConfig.Raw(), updatedConfig.ResolvedRaw())
	require.Equal(t, azdConfig.Raw(), updatedConfig.Raw())
	require.Equal(t, azdConfig.ResolvedRaw(), updatedConfig.ResolvedRaw())

	// verify vault reference
	vault, exists := azdConfig.GetString(vaultKeyName)
	require.True(t, exists)
	require.NotEmpty(t, vault)
	configFromRaw := NewConfig(azdConfig.ResolvedRaw())
	vault, exists = configFromRaw.GetString(vaultKeyName)
	require.False(t, exists)
	require.Empty(t, vault)
}

func Test_FileConfigManager_GetSetSecretsInSection(t *testing.T) {
	tempDir := t.TempDir()
	azdConfigDir := filepath.Join(tempDir, ".azd")

	t.Setenv("AZD_CONFIG_DIR", azdConfigDir)

	// Set and save secrets
	configFilePath := filepath.Join(tempDir, "config.json")
	configManager := NewFileConfigManager(NewManager())
	azdConfig := NewConfig(nil)

	err := azdConfig.SetSecret("infra.provisioning.secret1", "secrect1Value")
	require.NoError(t, err)

	err = azdConfig.SetSecret("infra.provisioning.secret2", "secrect2Value")
	require.NoError(t, err)

	err = azdConfig.Set("infra.provisioning.normalValue", "normalValue")
	require.NoError(t, err)

	err = configManager.Save(azdConfig, configFilePath)
	require.NoError(t, err)

	var provisioningParams map[string]string
	ok, err := azdConfig.GetSection("infra.provisioning", &provisioningParams)
	require.NoError(t, err)
	require.True(t, ok)

	secret1, ok := provisioningParams["secret1"]
	require.True(t, ok)
	require.Equal(t, "secrect1Value", secret1)

	secret2, ok := provisioningParams["secret2"]
	require.True(t, ok)
	require.Equal(t, "secrect2Value", secret2)

	normalValue, ok := provisioningParams["normalValue"]
	require.True(t, ok)
	require.Equal(t, "normalValue", normalValue)
}

func Test_FileConfigManager_VaultID_PathTraversal(t *testing.T) {
	tests := []struct {
		name    string
		vaultId string
		wantErr bool
	}{
		{"valid vault ID", "my-vault-123", false},
		{"double dot traversal", "../../../etc/shadow", true},
		{"forward slash", "path/to/vault", true},
		{"backslash", `path\to\vault`, true},
		{"embedded double dot", "vault..id", true},
		{"clean vault ID with dash and underscore", "vault-id_123", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			configFilePath := filepath.Join(tempDir, "config.json")
			configManager := NewFileConfigManager(NewManager())

			// Create a config with the vault reference
			azdConfig := NewConfig(map[string]any{
				vaultKeyName: tt.vaultId,
			})

			err := configManager.Save(azdConfig, configFilePath)
			require.NoError(t, err)
			// Load will validate the vault ID
			if tt.wantErr {
				// For save: the vaultId in the raw config won't trigger Save's vault path,
				// but Load will reject it
				_, err = configManager.Load(configFilePath)
				require.Error(t, err)
				require.Contains(t, err.Error(), "invalid vault ID")
			} else {
				// Valid vault IDs may still fail if the vault file doesn't exist, but
				// they should NOT fail with "invalid vault ID"
				_, err = configManager.Load(configFilePath)
				if err != nil {
					require.NotContains(t, err.Error(), "invalid vault ID")
				}
			}
		})
	}
}

// Test_FileConfigManager_Save_VaultID_PathTraversal exercises the vault ID
// path-traversal guard inside Save (line 105-109 of file_config_manager.go).
// Save checks baseConfig.vaultId (the struct field), so we must set it directly
// on the *config struct along with a non-nil vault to trigger the vault-save branch.
func Test_FileConfigManager_Save_VaultID_PathTraversal(t *testing.T) {
	tests := []struct {
		name    string
		vaultId string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "traversal with parent directory sequence",
			vaultId: "../../../etc/passwd",
			wantErr: true,
			errMsg:  "invalid vault ID",
		},
		{
			name:    "bare double dot",
			vaultId: "..",
			wantErr: true,
			errMsg:  "invalid vault ID",
		},
		{
			name:    "forward slash in vault ID",
			vaultId: "malicious/vault",
			wantErr: true,
			errMsg:  "invalid vault ID",
		},
		{
			name:    "backslash in vault ID",
			vaultId: `malicious\vault`,
			wantErr: true,
			errMsg:  "invalid vault ID",
		},
		{
			name:    "valid vault ID succeeds",
			vaultId: "good-vault-id-123",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			azdConfigDir := filepath.Join(tempDir, ".azd")

			t.Setenv("AZD_CONFIG_DIR", azdConfigDir)

			configFilePath := filepath.Join(tempDir, "config.json")
			configManager := NewFileConfigManager(NewManager())

			// Build a config with the vaultId struct field set directly.
			// This is the field Save checks (line 104), not the "vault" key in data.
			cfg := &config{
				vaultId: tt.vaultId,
				vault:   NewConfig(map[string]any{"secret-key": "secret-value"}),
				data:    map[string]any{"key": "value"},
			}

			err := configManager.Save(cfg, configFilePath)

			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)

				// Verify no file was written to the vaults directory for malicious IDs
				vaultsDir := filepath.Join(azdConfigDir, "vaults")
				if _, statErr := os.Stat(vaultsDir); statErr == nil {
					entries, _ := os.ReadDir(vaultsDir)
					require.Empty(t, entries, "no vault files should be created for malicious vault IDs")
				}
			} else {
				require.NoError(t, err)

				// Verify the vault file was written to the expected location
				expectedVaultPath := filepath.Join(azdConfigDir, "vaults", fmt.Sprintf("%s.json", tt.vaultId))
				require.FileExists(t, expectedVaultPath)
			}
		})
	}
}

// Test_FileConfigManager_Save_Atomic verifies that Save uses write-to-temp-then-rename
// so that no partial file is left behind. After Save, the file should contain valid JSON
// and no temporary files should remain in the directory.
func Test_FileConfigManager_Save_Atomic(t *testing.T) {
	tempDir := t.TempDir()
	configFilePath := filepath.Join(tempDir, "config.json")
	configManager := NewFileConfigManager(NewManager())

	azdConfig := NewConfig(map[string]any{
		"defaults": map[string]any{
			"location":     "westus2",
			"subscription": "sub-id-123",
		},
	})

	err := configManager.Save(azdConfig, configFilePath)
	require.NoError(t, err)

	// Verify the file exists and is valid
	loadedConfig, err := configManager.Load(configFilePath)
	require.NoError(t, err)
	require.NotNil(t, loadedConfig)
	require.Equal(t, azdConfig, loadedConfig)

	// Verify no temp files remain in the directory
	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(
			t,
			filepath.Ext(entry.Name()) == ".tmp",
			"temporary file should not remain: %s", entry.Name(),
		)
	}
}

// Test_FileConfigManager_Save_Overwrite verifies that Save atomically
// replaces an existing config file without data loss.
func Test_FileConfigManager_Save_Overwrite(t *testing.T) {
	tempDir := t.TempDir()
	configFilePath := filepath.Join(tempDir, "config.json")
	configManager := NewFileConfigManager(NewManager())

	// Write initial config
	initialConfig := NewConfig(map[string]any{
		"key": "initial-value",
	})
	err := configManager.Save(initialConfig, configFilePath)
	require.NoError(t, err)

	// Overwrite with new config
	updatedConfig := NewConfig(map[string]any{
		"key": "updated-value",
	})
	err = configManager.Save(updatedConfig, configFilePath)
	require.NoError(t, err)

	// Verify updated content
	loaded, err := configManager.Load(configFilePath)
	require.NoError(t, err)

	val, ok := loaded.GetString("key")
	require.True(t, ok)
	require.Equal(t, "updated-value", val)
}
