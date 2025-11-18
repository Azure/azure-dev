// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"
	"github.com/azure/azure-dev/pkg/config"
	"github.com/azure/azure-dev/pkg/convert"
	"github.com/stretchr/testify/require"
)

func Test_ParseDeploymentStackOptions(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		actual, err := parseDeploymentStackOptions(nil)

		require.NoError(t, err)
		require.Equal(t, defaultDeploymentStackOptions, actual)
	})

	t.Run("empty deployment stack options", func(t *testing.T) {
		// Options does not contain a 'deploymentStacks' key.
		infraOptions := map[string]any{}
		actual, err := parseDeploymentStackOptions(infraOptions)

		require.NoError(t, err)
		require.Equal(t, defaultDeploymentStackOptions, actual)
	})

	t.Run("invalid deployment stack options", func(t *testing.T) {
		config := config.NewConfig(nil)
		err := config.Set(deploymentStacksConfigKey, "invalid")
		require.NoError(t, err)

		actual, err := parseDeploymentStackOptions(config.Raw())
		require.Error(t, err)
		require.Nil(t, actual)
	})

	t.Run("override action on unmanage", func(t *testing.T) {
		customOptions := &deploymentStackOptions{
			ActionOnUnmanage: &armdeploymentstacks.ActionOnUnmanage{
				ManagementGroups: to.Ptr(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
				ResourceGroups:   to.Ptr(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
				Resources:        to.Ptr(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
			},
		}

		deploymentStacksMap, err := convert.ToMap(customOptions)
		require.NoError(t, err)

		config := config.NewConfig(nil)
		err = config.Set(deploymentStacksConfigKey, deploymentStacksMap)
		require.NoError(t, err)

		actual, err := parseDeploymentStackOptions(config.Raw())
		require.NoError(t, err)
		require.Equal(t, defaultDeploymentStackOptions.BypassStackOutOfSyncError, actual.BypassStackOutOfSyncError)
		require.Equal(t, customOptions.ActionOnUnmanage, actual.ActionOnUnmanage)
		require.Equal(t, defaultDeploymentStackOptions.DenySettings, actual.DenySettings)
	})

	t.Run("override deny settings", func(t *testing.T) {
		customOptions := &deploymentStackOptions{
			DenySettings: &armdeploymentstacks.DenySettings{
				Mode:               to.Ptr(armdeploymentstacks.DenySettingsModeDenyDelete),
				ApplyToChildScopes: to.Ptr(true),
				ExcludedPrincipals: []*string{
					to.Ptr("principal1"),
					to.Ptr("principal2"),
				},
			},
		}

		deploymentStacksMap, err := convert.ToMap(customOptions)
		require.NoError(t, err)

		config := config.NewConfig(nil)
		err = config.Set(deploymentStacksConfigKey, deploymentStacksMap)
		require.NoError(t, err)

		actual, err := parseDeploymentStackOptions(config.Raw())
		require.NoError(t, err)
		require.Equal(t, defaultDeploymentStackOptions.BypassStackOutOfSyncError, actual.BypassStackOutOfSyncError)
		require.Equal(t, defaultDeploymentStackOptions.ActionOnUnmanage, actual.ActionOnUnmanage)
		require.Equal(t, customOptions.DenySettings, actual.DenySettings)
	})

	t.Run("override all settings", func(t *testing.T) {
		customOptions := &deploymentStackOptions{
			BypassStackOutOfSyncError: to.Ptr(true),
			ActionOnUnmanage: &armdeploymentstacks.ActionOnUnmanage{
				ManagementGroups: to.Ptr(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
				ResourceGroups:   to.Ptr(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
				Resources:        to.Ptr(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
			},
			DenySettings: &armdeploymentstacks.DenySettings{
				Mode:               to.Ptr(armdeploymentstacks.DenySettingsModeDenyDelete),
				ApplyToChildScopes: to.Ptr(true),
				ExcludedPrincipals: []*string{
					to.Ptr("principal1"),
					to.Ptr("principal2"),
				},
			},
		}

		deploymentStacksMap, err := convert.ToMap(customOptions)
		require.NoError(t, err)

		config := config.NewConfig(nil)
		err = config.Set(deploymentStacksConfigKey, deploymentStacksMap)
		require.NoError(t, err)

		actual, err := parseDeploymentStackOptions(config.Raw())
		require.NoError(t, err)
		require.Equal(t, customOptions.BypassStackOutOfSyncError, actual.BypassStackOutOfSyncError)
		require.Equal(t, customOptions.ActionOnUnmanage, actual.ActionOnUnmanage)
		require.Equal(t, customOptions.DenySettings, actual.DenySettings)
	})

	t.Run("override bypass stack out of sync error from OS env var", func(t *testing.T) {
		t.Run("valid", func(t *testing.T) {
			err := os.Setenv(bypassOutOfSyncErrorEnvVarName, "true")
			require.NoError(t, err)

			config := config.NewConfig(nil)
			actual, err := parseDeploymentStackOptions(config.Raw())
			require.NoError(t, err)
			require.True(t, *actual.BypassStackOutOfSyncError)
			require.Equal(t, defaultDeploymentStackOptions.ActionOnUnmanage, actual.ActionOnUnmanage)
			require.Equal(t, defaultDeploymentStackOptions.DenySettings, actual.DenySettings)
		})
		t.Run("invalid", func(t *testing.T) {
			err := os.Setenv(bypassOutOfSyncErrorEnvVarName, "invalid")
			require.NoError(t, err)

			config := config.NewConfig(nil)
			actual, err := parseDeploymentStackOptions(config.Raw())
			require.NoError(t, err)
			require.Equal(t, defaultDeploymentStackOptions.BypassStackOutOfSyncError, actual.BypassStackOutOfSyncError)
			require.Equal(t, defaultDeploymentStackOptions.ActionOnUnmanage, actual.ActionOnUnmanage)
			require.Equal(t, defaultDeploymentStackOptions.DenySettings, actual.DenySettings)
		})
	})
}
