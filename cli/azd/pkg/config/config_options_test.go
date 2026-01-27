// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetAllConfigOptions(t *testing.T) {
	options := GetAllConfigOptions()

	// Should have at least some options
	require.NotEmpty(t, options)
	require.Greater(t, len(options), 0)

	// Check that we have the expected default options
	foundDefaultsSubscription := false
	foundDefaultsLocation := false
	foundAlphaAll := false
	foundAuthUseAzCliAuth := false
	foundPlatformType := false
	foundPlatformConfig := false
	foundCloudName := false
	foundAgentModelType := false

	for _, option := range options {
		require.NotEmpty(t, option.Key, "Config option key should not be empty")
		require.NotEmpty(t, option.Description, "Config option description should not be empty")
		require.NotEmpty(t, option.Type, "Config option type should not be empty")

		switch option.Key {
		case "defaults.subscription":
			foundDefaultsSubscription = true
			require.Equal(t, "string", option.Type)
			require.Contains(t, option.Description, "subscription")
		case "defaults.location":
			foundDefaultsLocation = true
			require.Equal(t, "string", option.Type)
			require.Contains(t, option.Description, "location")
		case "alpha.all":
			foundAlphaAll = true
			require.Equal(t, "string", option.Type)
			require.Contains(t, option.AllowedValues, "on")
			require.Contains(t, option.AllowedValues, "off")
			require.Equal(t, "AZD_ALPHA_ENABLE_ALL", option.EnvVar)
		case "auth.useAzCliAuth":
			foundAuthUseAzCliAuth = true
			require.Equal(t, "string", option.Type)
		case "platform.type":
			foundPlatformType = true
			require.Equal(t, "string", option.Type)
		case "platform.config":
			foundPlatformConfig = true
			require.Equal(t, "object", option.Type)
		case "cloud.name":
			foundCloudName = true
			require.Equal(t, "string", option.Type)
		case "ai.agent.model.type":
			foundAgentModelType = true
			require.Equal(t, "string", option.Type)
		}
	}

	// Verify expected options are present
	require.True(t, foundDefaultsSubscription, "defaults.subscription option should be present")
	require.True(t, foundDefaultsLocation, "defaults.location option should be present")
	require.True(t, foundAlphaAll, "alpha.all option should be present")
	require.True(t, foundAuthUseAzCliAuth, "auth.useAzCliAuth option should be present")
	require.True(t, foundPlatformType, "platform.type option should be present")
	require.True(t, foundPlatformConfig, "platform.config option should be present")
	require.True(t, foundCloudName, "cloud.name option should be present")
	require.True(t, foundAgentModelType, "ai.agent.model.type option should be present")
}

func TestConfigOptionStructure(t *testing.T) {
	options := GetAllConfigOptions()

	for _, option := range options {
		// All options should have required fields
		require.NotEmpty(t, option.Key)
		require.NotEmpty(t, option.Description)
		require.NotEmpty(t, option.Type)

		// If AllowedValues is set, it should not be empty
		if len(option.AllowedValues) > 0 {
			for _, val := range option.AllowedValues {
				require.NotEmpty(t, val, "Allowed value should not be empty")
			}
		}
	}
}
