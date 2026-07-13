// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

// Test the edge case of empty kind

// Test edge cases

func Test_BuiltInServiceTargetNames(t *testing.T) {
	names := builtInServiceTargetNames()
	require.NotEmpty(t, names)

	assert.Contains(t, names, "appservice")
	assert.Contains(t, names, "containerapp")
	assert.Contains(t, names, "function")
	assert.Contains(t, names, "staticwebapp")
	assert.Contains(t, names, "aks")
	assert.Contains(t, names, "ai.endpoint")
}

func Test_ParseServiceHost(t *testing.T) {
	t.Run("valid kinds", func(t *testing.T) {
		kinds := []ServiceTargetKind{
			AppServiceTarget, ContainerAppTarget, AzureFunctionTarget,
			StaticWebAppTarget, AksTarget, AiEndpointTarget,
		}
		for _, kind := range kinds {
			result, err := parseServiceHost(kind)
			require.NoError(t, err)
			assert.Equal(t, kind, result)
		}
	})

	t.Run("empty host returns error", func(t *testing.T) {
		_, err := parseServiceHost(ServiceTargetKind(""))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "host cannot be empty")
	})

	t.Run("custom/extension host allowed", func(t *testing.T) {
		result, err := parseServiceHost(ServiceTargetKind("custom-extension"))
		require.NoError(t, err)
		assert.Equal(t, ServiceTargetKind("custom-extension"), result)
	})
}

func Test_ResourceTypeMismatchError(t *testing.T) {
	err := resourceTypeMismatchError("myResource", "Microsoft.Web/sites", "Microsoft.App/containerApps")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "myResource")
	assert.Contains(t, err.Error(), "Microsoft.Web/sites")
	assert.Contains(t, err.Error(), "Microsoft.App/containerApps")
}

func Test_CheckResourceType(t *testing.T) {
	t.Run("matching type", func(t *testing.T) {
		resource := environment.NewTargetResource("sub", "rg", "myApp", "Microsoft.Web/sites")
		err := checkResourceType(resource, "Microsoft.Web/sites")
		require.NoError(t, err)
	})

	t.Run("case insensitive match", func(t *testing.T) {
		resource := environment.NewTargetResource("sub", "rg", "myApp", "microsoft.web/sites")
		err := checkResourceType(resource, "Microsoft.Web/sites")
		require.NoError(t, err)
	})

	t.Run("mismatched type", func(t *testing.T) {
		resource := environment.NewTargetResource("sub", "rg", "myApp", "Microsoft.Web/sites")
		err := checkResourceType(resource, "Microsoft.App/containerApps")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not match")
	})
}

func Test_NewExternalServiceTarget(t *testing.T) {
	target := NewExternalServiceTarget("test-target", ContainerAppTarget, nil, nil, nil, nil, nil)
	require.NotNil(t, target)
}

// ---------- IgnoreFile method coverage for different targets ----------
func Test_ServiceTargetKind_IgnoreFile_Extended(t *testing.T) {
	assert.Equal(t, ".webappignore", AppServiceTarget.IgnoreFile())
	assert.Equal(t, ".funcignore", AzureFunctionTarget.IgnoreFile())
	assert.Equal(t, "", ContainerAppTarget.IgnoreFile())
	assert.Equal(t, "", StaticWebAppTarget.IgnoreFile())
	assert.Equal(t, "", AksTarget.IgnoreFile())
}

// ---------- SupportsDelayedProvisioning ----------
func Test_ServiceTargetKind_SupportsDelayedProvisioning_Extended(t *testing.T) {
	assert.True(t, AksTarget.SupportsDelayedProvisioning())
	assert.False(t, AppServiceTarget.SupportsDelayedProvisioning())
	assert.False(t, ContainerAppTarget.SupportsDelayedProvisioning())
}
