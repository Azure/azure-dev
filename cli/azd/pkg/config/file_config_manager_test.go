package config

import (
	"path/filepath"
	"testing"

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

func Test_Secrets_GetSet(t *testing.T) {
	configFilePath := filepath.Join(t.TempDir(), "config.json")
	configManager := NewFileConfigManager(NewManager())
	azdConfig := NewConfig(nil)

	expectedPassword := "P@55w0rd!"
	err := azdConfig.SetSecret("secrets.password", expectedPassword)
	require.NoError(t, err)

	err = configManager.Save(azdConfig, configFilePath)
	require.NoError(t, err)

	updatedConfig, err := configManager.Load(configFilePath)
	require.NoError(t, err)
	require.NotNil(t, updatedConfig)

	value, ok := updatedConfig.GetSecret("secrets.password")
	require.True(t, ok)
	require.Equal(t, expectedPassword, value)
}
