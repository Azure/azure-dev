// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
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

	mockContext := mocks.NewMockContext(context.Background())
	root := t.TempDir()

	envManager, _ := createEnvManager(t, mockContext, root)

	// Create a new config from an empty root.
	env := New("test")

	// There should be no configuration since this is an empty environment.
	require.True(t, env.Config.IsEmpty())

	// Set a config value.
	err := env.Config.Set("is.this.a.test", true)
	require.NoError(t, err)

	// Save the environment
	err = envManager.Save(*mockContext.Context, env)
	require.NoError(t, err)

	// Load the environment back up, we expect no error and for the config value we wrote to still exist.
	env, err = envManager.Get(*mockContext.Context, "test")
	require.NoError(t, err)
	v, has := env.Config.Get("is.this.a.test")
	require.True(t, has)
	require.Equal(t, true, v)
}

func TestFromRoot(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	t.Run("EmptyRoot", func(t *testing.T) {
		t.Parallel()

		env := New("test")
		require.NotNil(t, env)

		require.NotNil(t, env.Config)
		require.NotNil(t, env.dotenv)

		require.NotNil(t, env.Config.IsEmpty())
		require.Equal(t, 1, len(env.dotenv))
	})

	t.Run("EmptyWhenMissing", func(t *testing.T) {
		t.Parallel()

		envManager, _ := createEnvManager(t, mockContext, t.TempDir())
		env, err := envManager.Get(*mockContext.Context, "test")
		require.ErrorIs(t, err, ErrNotFound)
		require.Nil(t, env)
	})

	// Simulate loading an environment written by an earlier version of `azd` which did not write `config.json`. We should
	// still be able to load the environment without error, and the Config object should be valid but empty. Any existing
	// entries in the .env file should be present when we load the existing environment.
	t.Run("Upgrade", func(t *testing.T) {
		t.Parallel()

		envManager, azdCtx := createEnvManager(t, mockContext, t.TempDir())
		envRoot := azdCtx.EnvironmentRoot("testEnv")

		err := os.MkdirAll(envRoot, osutil.PermissionDirectory)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(envRoot, ".env"), []byte("TEST=yes\n"), osutil.PermissionFile)
		require.NoError(t, err)

		env, err := envManager.Get(*mockContext.Context, "testEnv")
		require.NoError(t, err)

		require.Equal(t, "yes", env.dotenv["TEST"])
		require.True(t, env.Config.IsEmpty())
	})
}

func Test_SaveAndReload(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	envManager, azdCtx := createEnvManager(t, mockContext, t.TempDir())

	env := New("test")
	require.NotNil(t, env)

	env.SetLocation("eastus2")
	env.SetSubscriptionId("SUBSCRIPTION_ID")

	err := envManager.Save(*mockContext.Context, env)
	require.NoError(t, err)

	// Simulate another process writing to .env file
	envRoot := azdCtx.EnvironmentRoot("test")
	envPath := filepath.Join(envRoot, azdcontext.DotEnvFileName)
	envMap, err := godotenv.Read(envPath)
	require.NotNil(t, envMap)
	require.NoError(t, err)

	// This entry does not exist in the current env state but is added as part of the reload process
	envMap["SERVICE_API_ENDPOINT_URL"] = "http://api.example.com"
	err = godotenv.Write(envMap, envPath)
	require.NoError(t, err)

	err = envManager.Reload(*mockContext.Context, env)
	require.NoError(t, err)

	// Set a new property in the env
	env.SetServiceProperty("web", "ENDPOINT_URL", "http://web.example.com")
	err = envManager.Save(*mockContext.Context, env)
	require.NoError(t, err)

	// Verify all values exist with expected values
	// All values now exist whether or not they were in the env state to start with
	require.Equal(t, env.dotenv["SERVICE_WEB_ENDPOINT_URL"], "http://web.example.com")
	require.Equal(t, env.dotenv["SERVICE_API_ENDPOINT_URL"], "http://api.example.com")
	require.Equal(t, "SUBSCRIPTION_ID", env.GetSubscriptionId())
	require.Equal(t, "eastus2", env.GetLocation())

	// Delete the newly added property
	env.DotenvDelete("SERVICE_WEB_ENDPOINT_URL")
	err = envManager.Save(*mockContext.Context, env)
	require.NoError(t, err)

	// Verify the property is deleted
	_, ok := env.LookupEnv("SERVICE_WEB_ENDPOINT_URL")
	require.False(t, ok)

	// Delete an existing key, then add it with a different value and save the environment, to ensure we
	// don't drop the existing key even though it was deleted in an earlier operation.
	env.DotenvDelete("SERVICE_API_ENDPOINT_URL")
	env.DotenvSet("SERVICE_API_ENDPOINT_URL", "http://api.example.com/updated")

	err = envManager.Save(*mockContext.Context, env)
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
	mockContext := mocks.NewMockContext(context.Background())
	envManager, _ := createEnvManager(t, mockContext, t.TempDir())
	env := New("test")
	env.DotenvSet("TEST", "01")
	err := envManager.Save(*mockContext.Context, env)
	require.NoError(t, err)

	env2, err := envManager.Get(*mockContext.Context, "test")
	require.NoError(t, err)
	require.Equal(t, "01", env2.dotenv["TEST"])
}

const configSample = `{
	"infra": {
	  "parameters": {
		"bro": "xms"
	  }
	}
  }`

func TestInitialEnvState(t *testing.T) {

	// expected config
	var configEncode map[string]any
	err := json.Unmarshal([]byte(configSample), &configEncode)
	require.NoError(t, err)
	configBytes, err := json.Marshal(configEncode)
	require.NoError(t, err)

	// Set up the environment variable
	t.Setenv(AzdInitialEnvironmentConfigName, string(configBytes))

	// Create the environment
	env := New("test")

	// pull config back and compare against expected
	config := env.Config.Raw()
	require.Equal(t, configEncode, config)
}

func TestInitialEnvStateWithError(t *testing.T) {

	// Set up the environment variable
	t.Setenv(AzdInitialEnvironmentConfigName, "not{}valid{}json")

	// Create the environment
	env := New("test")

	// pull unexpectedConfig back and compare
	unexpectedConfig := env.Config.Raw()
	expected := config.NewEmptyConfig().Raw()
	require.Equal(t, expected, unexpectedConfig)
}

func TestInitialEnvStateEmpty(t *testing.T) {

	// expected config
	expected := config.NewEmptyConfig().Raw()

	// Create the environment
	env := New("test")

	// pull config back and compare against expected
	config := env.Config.Raw()
	require.Equal(t, expected, config)
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

func createEnvManager(t *testing.T, mockContext *mocks.MockContext, root string) (Manager, *azdcontext.AzdContext) {
	azdCtx := azdcontext.NewAzdContextWithDirectory(root)
	configManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := NewLocalFileDataStore(azdCtx, configManager)

	return newManagerForTest(azdCtx, mockContext.Console, localDataStore, nil), azdCtx
}
