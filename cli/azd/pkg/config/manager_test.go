package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_SaveAndLoadConfig(t *testing.T) {
	defer deleteExistingConfig()

	var azdConfig Config = NewConfig(
		map[string]any{
			"defaults": map[string]any{
				"location":     "eastus2",
				"subscription": "SUBSCRIPTION_ID",
			},
		},
	)

	configFilePath, _ := GetUserConfigFilePath()
	configManager := NewManager()
	err := configManager.Save(azdConfig, configFilePath)
	require.NoError(t, err)

	existingConfig, err := configManager.Load(configFilePath)
	require.NoError(t, err)
	require.NotNil(t, existingConfig)
	require.Equal(t, azdConfig, existingConfig)
}

func Test_SaveAndLoadEmptyConfig(t *testing.T) {
	defer deleteExistingConfig()

	configFilePath, _ := GetUserConfigFilePath()
	configManager := NewManager()
	azdConfig := NewConfig(nil)
	err := configManager.Save(azdConfig, configFilePath)
	require.NoError(t, err)

	existingConfig, err := configManager.Load(configFilePath)
	require.NoError(t, err)
	require.NotNil(t, existingConfig)
}

func deleteExistingConfig() {
	configFilePath, _ := GetUserConfigFilePath()
	// Remove file if it exists
	_ = os.Remove(configFilePath)
}
