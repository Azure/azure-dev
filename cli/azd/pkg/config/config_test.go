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

	err := azdConfig.Save()
	require.NoError(t, err)

	existingConfig, err := Load()
	require.NoError(t, err)
	require.NotNil(t, existingConfig)
	require.Equal(t, azdConfig, existingConfig)
}

func Test_SaveAndLoadEmptyConfig(t *testing.T) {
	defer deleteExistingConfig()

	azdConfig := NewConfig(nil)
	err := azdConfig.Save()
	require.NoError(t, err)

	existingConfig, err := Load()
	require.NoError(t, err)
	require.NotNil(t, existingConfig)
}

func Test_SetGetUnsetWithValue(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		value any
	}{
		{
			name:  "RootValue",
			path:  "a",
			value: "apple",
		},
		{
			name:  "NestedValue",
			path:  "defaults.location",
			value: "westus",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			azdConfig := NewConfig(nil)
			err := azdConfig.Set(test.path, test.value)
			require.NoError(t, err)

			value, ok := azdConfig.Get(test.path)
			require.True(t, ok)
			require.Equal(t, test.value, value)

			err = azdConfig.Unset(test.path)
			require.NoError(t, err)

			value, ok = azdConfig.Get(test.path)
			require.Nil(t, value)
			require.False(t, ok)
		})
	}
}

func Test_SetGetUnsetRootNodeWithChildren(t *testing.T) {
	expectedLocation := "westus2"
	expectedEmail := "john.doe@contoso.com"

	azdConfig := NewConfig(nil)
	_ = azdConfig.Set("defaults.location", expectedLocation)
	_ = azdConfig.Set("defaults.subscription", "SUBSCRIPTION_ID")
	_ = azdConfig.Set("user.email", expectedEmail)

	location, ok := azdConfig.Get("defaults.location")
	require.True(t, ok)
	require.Equal(t, expectedLocation, location)

	email, ok := azdConfig.Get("user.email")
	require.True(t, ok)
	require.Equal(t, expectedEmail, email)

	// Remove the whole defaults object
	err := azdConfig.Unset("defaults")
	require.NoError(t, err)

	// Location should not exist
	location, ok = azdConfig.Get("defaults.location")
	require.False(t, ok)
	require.Nil(t, location)

	// user.email should still exist
	email, ok = azdConfig.Get("user.email")
	require.True(t, ok)
	require.Equal(t, expectedEmail, email)
}

func deleteExistingConfig() {
	configFilePath, _ := getConfigFilePath()
	// Remove file if it exists
	_ = os.Remove(configFilePath)
}
