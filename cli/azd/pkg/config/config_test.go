package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_SaveAndLoadConfig(t *testing.T) {
	defer deleteExistingConfig()

	newConfig := &Config{
		DefaultSubscription: &Subscription{
			Id:   "SUBSCRIPTION_ID",
			Name: "Test Subscription",
		},
		DefaultLocation: &Location{
			Name:        "eastus2",
			DisplayName: "East US 2",
		},
	}

	err := newConfig.Save()
	require.NoError(t, err)

	existingConfig, err := Load()
	require.NoError(t, err)
	require.NotNil(t, existingConfig)
	require.Equal(t, newConfig, existingConfig)
}

func Test_SaveAndLoadEmptyConfig(t *testing.T) {
	defer deleteExistingConfig()

	newConfig := &Config{}
	err := newConfig.Save()
	require.NoError(t, err)

	existingConfig, err := Load()
	require.NoError(t, err)
	require.NotNil(t, existingConfig)
	require.Nil(t, existingConfig.DefaultSubscription)
	require.Nil(t, existingConfig.DefaultLocation)
}

func deleteExistingConfig() {
	configFilePath, _ := getConfigFilePath()
	// Remove file if it exists
	_ = os.Remove(configFilePath)
}
