package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_SaveAndLoadConfig(t *testing.T) {
	var azdConfig Config = NewConfig(
		map[string]any{
			"defaults": map[string]any{
				"location":     "eastus2",
				"subscription": "SUBSCRIPTION_ID",
			},
		},
	)

	configFilePath := filepath.Join(t.TempDir(), "config.json")
	configManager := NewManager()
	err := configManager.Save(azdConfig, configFilePath)
	require.NoError(t, err)

	existingConfig, err := configManager.Load(configFilePath)
	require.NoError(t, err)
	require.NotNil(t, existingConfig)
	require.Equal(t, azdConfig, existingConfig)
}

func Test_SaveAndLoadEmptyConfig(t *testing.T) {
	configFilePath := filepath.Join(t.TempDir(), "config.json")
	configManager := NewManager()
	azdConfig := NewConfig(nil)
	err := configManager.Save(azdConfig, configFilePath)
	require.NoError(t, err)

	existingConfig, err := configManager.Load(configFilePath)
	require.NoError(t, err)
	require.NotNil(t, existingConfig)
}

func Test_DirectoryPermissions(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		// Run this test only on Linux/macOS
		return
	}

	getPermissions := func(t *testing.T, path string) fs.FileMode {
		info, err := os.Stat(path)
		require.NoError(t, err)

		return info.Mode().Perm()
	}

	testDir, err := os.MkdirTemp(os.TempDir(), "azd_config_dir_test*")
	require.NoError(t, err)

	// Remove the test directory to validate creation
	os.Remove(testDir)

	t.Setenv("AZD_CONFIG_DIR", testDir)
	configDir, err := GetUserConfigDir()
	require.NoError(t, err)
	require.DirExists(t, configDir)

	permissions := getPermissions(t, configDir)
	require.NotZero(t, permissions&100)

	// Ensure max permission is 0644 ()
	os.Chmod(configDir, permissions&0644)

	configDir, err = GetUserConfigDir()
	require.NoError(t, err)
	permissions = getPermissions(t, configDir)
	require.NotZero(t, permissions&100)

}
