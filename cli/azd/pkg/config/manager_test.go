package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
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

func Test_GetUserConfigDir(t *testing.T) {
	getPermissions := func(t *testing.T, path string) fs.FileMode {
		info, err := os.Stat(path)
		require.NoError(t, err)

		return info.Mode().Perm()
	}

	t.Run("Creates config directory at ~/.azd", func(t *testing.T) {
		// Default case: Returns config directory at ~/.azd
		// (This test case does NOT delete ~/.azd if it exists)
		configDir, err := GetUserConfigDir()
		require.NoError(t, err)
		require.DirExists(t, configDir)
	})

	t.Run("Creates config directory at AZD_CONFIG_DIR", func(t *testing.T) {
		// Setup temp directory for use in tests, delete after creation to validate
		// folder creation
		testDir := t.TempDir()
		err := os.RemoveAll(testDir)
		require.NoError(t, err)

		t.Setenv("AZD_CONFIG_DIR", testDir)
		configDir, err := GetUserConfigDir()
		require.NoError(t, err)
		require.Equal(t, testDir, configDir)
		require.DirExists(t, testDir)
	})

	t.Run("Creates config directory with correct permissions", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("skip file permission tests on Windows")
			return
		}

		// Setup temp directory for use in tests, delete after creation to validate
		// folder creation
		testDir := t.TempDir()
		err := os.RemoveAll(testDir)
		require.NoError(t, err)

		t.Setenv("AZD_CONFIG_DIR", testDir)
		configDir, err := GetUserConfigDir()
		require.NoError(t, err)

		// Directory permissions are set so directory can be accessed by
		// current user.
		permissions := getPermissions(t, configDir)
		require.NotZero(t, permissions&osutil.PermissionMaskDirectoryExecute)
	})

	t.Run("Updates permissions if not correct", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("skip file permission tests on Windows")
			return
		}

		// Setup temp directory for use in tests, delete after creation to validate
		// folder creation
		testDir := t.TempDir()
		err := os.RemoveAll(testDir)
		require.NoError(t, err)

		// Setup: Ensure user does not have "x" permission on the configDir
		// Permissions 0644 (rw-r--r--)
		err = os.MkdirAll(testDir, osutil.PermissionFile)
		require.NoError(t, err)
		t.Setenv("AZD_CONFIG_DIR", testDir)

		configDir, err := GetUserConfigDir()
		require.NoError(t, err)

		permissions := getPermissions(t, configDir)
		// Ensure permissions for user are "rwx" (user has access to directory)
		require.NotZero(t, permissions&osutil.PermissionMaskDirectoryExecute)
	})
}
