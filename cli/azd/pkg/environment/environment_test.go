// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsValidEnvironmentName(t *testing.T) {
	assert.True(t, IsValidEnvironmentName("simple"))
	assert.True(t, IsValidEnvironmentName("a-name-with-hyphens"))
	assert.True(t, IsValidEnvironmentName("C()mPl3x_ExAmPl3-ThatIsVeryLong"))

	assert.False(t, IsValidEnvironmentName(""))
	assert.False(t, IsValidEnvironmentName("no*allowed"))
	assert.False(t, IsValidEnvironmentName("no spaces"))
	assert.False(t, IsValidEnvironmentName("12345678901234567890123456789012345678901234567890123456789012345"))
}

func TestConfigRoundTrips(t *testing.T) {
	root := t.TempDir()

	// Create a new config from an empty root. We expect this to fail (because there is no configuration data), but
	// to get back an empty configuration object we can use regardless.
	e, err := FromRoot(root)
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))

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
