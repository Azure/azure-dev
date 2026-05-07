// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ServiceTargetKind_RequiresContainer_Coverage3(t *testing.T) {
	// Test the edge case of empty kind
	assert.False(t, ServiceTargetKind("").RequiresContainer())
	assert.False(t, ServiceTargetKind("custom-host").RequiresContainer())
}

func Test_ServiceTargetKind_IgnoreFile_Coverage3(t *testing.T) {
	// Test edge cases
	assert.Equal(t, "", ServiceTargetKind("").IgnoreFile())
	assert.Equal(t, "", ServiceTargetKind("custom-host").IgnoreFile())
}

func Test_ServiceTargetKind_SupportsDelayedProvisioning_Coverage3(t *testing.T) {
	tests := []struct {
		kind   ServiceTargetKind
		expect bool
	}{
		{AksTarget, true},
		{ContainerAppTarget, false},
		{AppServiceTarget, false},
		{AzureFunctionTarget, false},
		{StaticWebAppTarget, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			assert.Equal(t, tt.expect, tt.kind.SupportsDelayedProvisioning())
		})
	}
}

func Test_BuiltInServiceTargetKinds_Coverage3(t *testing.T) {
	kinds := BuiltInServiceTargetKinds()
	require.NotEmpty(t, kinds)

	assert.Contains(t, kinds, AppServiceTarget)
	assert.Contains(t, kinds, ContainerAppTarget)
	assert.Contains(t, kinds, AzureFunctionTarget)
	assert.Contains(t, kinds, StaticWebAppTarget)
	assert.Contains(t, kinds, AksTarget)
	assert.Contains(t, kinds, AiEndpointTarget)
}

func Test_BuiltInServiceTargetNames_Coverage3(t *testing.T) {
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
