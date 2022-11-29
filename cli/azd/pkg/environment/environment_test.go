// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
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
		require.NotNil(t, e.Values)

		require.NotNil(t, e.Config.IsEmpty())
		require.Equal(t, 0, len(e.Values))
	})

	t.Run("EmptyWhenMissing", func(t *testing.T) {
		t.Parallel()

		e, err := FromRoot(filepath.Join(t.TempDir(), "test"))
		require.ErrorIs(t, err, os.ErrNotExist)
		require.NotNil(t, e)

		require.NotNil(t, e.Config)
		require.NotNil(t, e.Values)

		require.NotNil(t, e.Config.IsEmpty())
		require.Equal(t, 0, len(e.Values))
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

		require.Equal(t, "yes", e.Values["TEST"])
		require.True(t, e.Config.IsEmpty())
	})
}
