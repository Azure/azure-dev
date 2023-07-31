// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsValidEnvironmentName(t *testing.T) {
	t.Parallel()

	assert.True(t, IsValidEnvironmentName("simple"))
	assert.True(t, IsValidEnvironmentName("a-name-with-hyphens"))
	assert.True(t, IsValidEnvironmentName("C()mPl3x_ExAmPl3-ThatIsVeryLong"))

	assert.False(t, IsValidEnvironmentName(""))
	assert.False(t, IsValidEnvironmentName("no*allowed"))
	assert.False(t, IsValidEnvironmentName("no spaces"))
	assert.False(t, IsValidEnvironmentName("12345678901234567890123456789012345678901234567890123456789012345"))
}

func TestConfigRoundTrips(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Create a new config from an empty root.
	e, err := FromRoot(root)
	require.NoError(t, err)

	// There should be no configuration since this is an empty environment.
	require.True(t, e.Config.IsEmpty())

	// Set a config value.
	err = e.Config.Set("is.this.a.test", true)
	require.NoError(t, err)

	// Save the environment
	err = e.Save()
	require.NoError(t, err)

	// Load the environment back up, we expect no error and for the config value we wrote to still exist.
	e, err = FromRoot(root)
	require.NoError(t, err)
	v, has := e.Config.Get("is.this.a.test")
	require.True(t, has)
	require.Equal(t, true, v)
}

func TestFromRoot(t *testing.T) {
	t.Parallel()

	t.Run("EmptyRoot", func(t *testing.T) {
		t.Parallel()

		e, err := FromRoot(t.TempDir())
		require.NoError(t, err)
		require.NotNil(t, e)

		require.NotNil(t, e.Config)
		require.NotNil(t, e.dotenv)

		require.NotNil(t, e.Config.IsEmpty())
		require.Equal(t, 0, len(e.dotenv))
	})

	t.Run("EmptyWhenMissing", func(t *testing.T) {
		t.Parallel()

		e, err := FromRoot(filepath.Join(t.TempDir(), "test"))
		require.ErrorIs(t, err, os.ErrNotExist)
		require.NotNil(t, e)

		require.NotNil(t, e.Config)
		require.NotNil(t, e.dotenv)

		require.NotNil(t, e.Config.IsEmpty())
		require.Equal(t, 0, len(e.dotenv))
	})

	// Simulate loading an environment written by an earlier version of `azd` which did not write `config.json`. We should
	// still be able to load the environment without error, and the Config object should be valid but empty. Any existing
	// entries in the .env file should be present when we load the existing environment.
	t.Run("Upgrade", func(t *testing.T) {
		t.Parallel()

		testRoot := filepath.Join(t.TempDir(), "testEnv")

		err := os.MkdirAll(testRoot, osutil.PermissionDirectory)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(testRoot, ".env"), []byte("TEST=yes\n"), osutil.PermissionFile)
		require.NoError(t, err)

		e, err := FromRoot(testRoot)
		require.NoError(t, err)

		require.Equal(t, "yes", e.dotenv["TEST"])
		require.True(t, e.Config.IsEmpty())
	})
}

func Test_SaveAndReload(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	env := EmptyWithRoot(tempDir)
	require.NotNil(t, env)

	env.SetLocation("eastus2")
	env.SetSubscriptionId("SUBSCRIPTION_ID")

	err := env.Save()
	require.NoError(t, err)

	// Simulate another process writing to .env file
	envPath := filepath.Join(tempDir, azdcontext.DotEnvFileName)
	envMap, err := godotenv.Read(envPath)
	require.NotNil(t, envMap)
	require.NoError(t, err)

	// This entry does not exist in the current env state but is added as part of the reload process
	envMap["SERVICE_API_ENDPOINT_URL"] = "http://api.example.com"
	err = godotenv.Write(envMap, envPath)
	require.NoError(t, err)

	err = env.Reload()
	require.NoError(t, err)

	// Set a new property in the env
	env.SetServiceProperty("web", "ENDPOINT_URL", "http://web.example.com")
	err = env.Save()
	require.NoError(t, err)

	// Verify all values exist with expected values
	// All values now exist whether or not they were in the env state to start with
	require.Equal(t, env.dotenv["SERVICE_WEB_ENDPOINT_URL"], "http://web.example.com")
	require.Equal(t, env.dotenv["SERVICE_API_ENDPOINT_URL"], "http://api.example.com")
	require.Equal(t, "SUBSCRIPTION_ID", env.GetSubscriptionId())
	require.Equal(t, "eastus2", env.GetLocation())

	// Delete the newly added property
	env.DotenvDelete("SERVICE_WEB_ENDPOINT_URL")
	err = env.Save()
	require.NoError(t, err)

	// Verify the property is deleted
	_, ok := env.LookupEnv("SERVICE_WEB_ENDPOINT_URL")
	require.False(t, ok)

	// Delete an existing key, then add it with a different value and save the environment, to ensure we
	// don't drop the existing key even though it was deleted in an earlier operation.
	env.DotenvDelete("SERVICE_API_ENDPOINT_URL")
	env.DotenvSet("SERVICE_API_ENDPOINT_URL", "http://api.example.com/updated")

	err = env.Save()
	require.NoError(t, err)

	// Verify the property still exists, and has the updated value.
	value, ok := env.LookupEnv("SERVICE_API_ENDPOINT_URL")
	require.True(t, ok)
	require.Equal(t, "http://api.example.com/updated", value)
}

func TestCleanName(t *testing.T) {
	require.Equal(t, "already-clean-name", CleanName("already-clean-name"))
	require.Equal(t, "was-CLEANED-with--bad--things-(123)", CleanName("was CLEANED with *bad* things (123)"))
}

func TestRoundTripNumberWithLeadingZeros(t *testing.T) {
	root := t.TempDir()
	e := EmptyWithRoot(root)
	e.DotenvSet("TEST", "01")
	err := e.Save()
	require.NoError(t, err)

	e2, err := FromRoot(root)
	require.NoError(t, err)
	require.Equal(t, "01", e2.dotenv["TEST"])
}

func Test_fixupUnquotedDotenv(t *testing.T) {
	test := map[string]string{
		"TEST_SHOULD_NOT_QUOTE": "1",
		"TEST_SHOULD_QUOTE":     "01",
	}

	dotenv, err := godotenv.Marshal(test)
	require.NoError(t, err)
	require.Equal(t, "TEST_SHOULD_NOT_QUOTE=1\nTEST_SHOULD_QUOTE=1", dotenv)

	fixed := fixupUnquotedDotenv(test, dotenv)
	require.Equal(t, "TEST_SHOULD_NOT_QUOTE=1\nTEST_SHOULD_QUOTE=\"01\"", fixed)
}

func Test_Environment_Path(t *testing.T) {
	root := t.TempDir()
	env := EmptyWithRoot(root)

	path := env.Path()
	require.Equal(t, filepath.Join(root, azdcontext.DotEnvFileName), path)
}

func Test_Environment_ConfigPath(t *testing.T) {
	root := t.TempDir()
	env := EmptyWithRoot(root)

	path := env.ConfigPath()
	require.Equal(t, filepath.Join(root, azdcontext.ConfigFileName), path)
}
