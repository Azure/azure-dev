// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/require"
)

func Test_BuiltInServiceTargetKinds(t *testing.T) {
	kinds := BuiltInServiceTargetKinds()

	require.NotEmpty(t, kinds)
	require.Contains(t, kinds, AppServiceTarget)
	require.Contains(t, kinds, ContainerAppTarget)
	require.Contains(t, kinds, AzureFunctionTarget)
	require.Contains(t, kinds, StaticWebAppTarget)
	require.Contains(t, kinds, AksTarget)
	require.Contains(t, kinds, AiEndpointTarget)

	// DotNetContainerAppTarget and SpringAppTarget are
	// intentionally excluded from the built-in list.
	require.NotContains(t, kinds, DotNetContainerAppTarget)
	require.NotContains(t, kinds, SpringAppTarget)
}

func Test_builtInServiceTargetNames(t *testing.T) {
	names := builtInServiceTargetNames()
	kinds := BuiltInServiceTargetKinds()

	require.Len(t, names, len(kinds))
	for i, kind := range kinds {
		require.Equal(t, string(kind), names[i])
	}
}

func Test_ServiceTargetKind_RequiresContainer(t *testing.T) {
	tests := []struct {
		name     string
		kind     ServiceTargetKind
		expected bool
	}{
		{
			name:     "ContainerAppTarget requires container",
			kind:     ContainerAppTarget,
			expected: true,
		},
		{
			name:     "AksTarget requires container",
			kind:     AksTarget,
			expected: true,
		},
		{
			name:     "AppServiceTarget does not",
			kind:     AppServiceTarget,
			expected: false,
		},
		{
			name:     "AzureFunctionTarget does not",
			kind:     AzureFunctionTarget,
			expected: false,
		},
		{
			name:     "StaticWebAppTarget does not",
			kind:     StaticWebAppTarget,
			expected: false,
		},
		{
			name:     "AiEndpointTarget does not",
			kind:     AiEndpointTarget,
			expected: false,
		},
		{
			name:     "DotNetContainerAppTarget does not",
			kind:     DotNetContainerAppTarget,
			expected: false,
		},
		{
			name:     "NonSpecifiedTarget does not",
			kind:     NonSpecifiedTarget,
			expected: false,
		},
		{
			name:     "Unknown kind does not",
			kind:     ServiceTargetKind("unknown"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.kind.RequiresContainer())
		})
	}
}

func Test_ServiceTargetKind_IgnoreFile(t *testing.T) {
	tests := []struct {
		name     string
		kind     ServiceTargetKind
		expected string
	}{
		{
			name:     "AppService returns .webappignore",
			kind:     AppServiceTarget,
			expected: ".webappignore",
		},
		{
			name:     "Function returns .funcignore",
			kind:     AzureFunctionTarget,
			expected: ".funcignore",
		},
		{
			name:     "ContainerApp returns empty",
			kind:     ContainerAppTarget,
			expected: "",
		},
		{
			name:     "AKS returns empty",
			kind:     AksTarget,
			expected: "",
		},
		{
			name:     "StaticWebApp returns empty",
			kind:     StaticWebAppTarget,
			expected: "",
		},
		{
			name:     "NonSpecified returns empty",
			kind:     NonSpecifiedTarget,
			expected: "",
		},
		{
			name:     "Unknown kind returns empty",
			kind:     ServiceTargetKind("custom-ext"),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.kind.IgnoreFile())
		})
	}
}

func Test_ServiceTargetKind_SupportsDelayedProvisioning(
	t *testing.T,
) {
	tests := []struct {
		name     string
		kind     ServiceTargetKind
		expected bool
	}{
		{
			name:     "AKS supports delayed provisioning",
			kind:     AksTarget,
			expected: true,
		},
		{
			name:     "ContainerApp does not",
			kind:     ContainerAppTarget,
			expected: false,
		},
		{
			name:     "AppService does not",
			kind:     AppServiceTarget,
			expected: false,
		},
		{
			name:     "Function does not",
			kind:     AzureFunctionTarget,
			expected: false,
		},
		{
			name:     "StaticWebApp does not",
			kind:     StaticWebAppTarget,
			expected: false,
		},
		{
			name:     "NonSpecified does not",
			kind:     NonSpecifiedTarget,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.kind.SupportsDelayedProvisioning()
			require.Equal(t, tt.expected, got)
		})
	}
}

func Test_parseServiceHost(t *testing.T) {
	tests := []struct {
		name      string
		kind      ServiceTargetKind
		expected  ServiceTargetKind
		expectErr bool
	}{
		{
			name:     "known kind appservice",
			kind:     AppServiceTarget,
			expected: AppServiceTarget,
		},
		{
			name:     "known kind containerapp",
			kind:     ContainerAppTarget,
			expected: ContainerAppTarget,
		},
		{
			name:     "known kind function",
			kind:     AzureFunctionTarget,
			expected: AzureFunctionTarget,
		},
		{
			name:     "extension kind passes through",
			kind:     ServiceTargetKind("my-custom-ext"),
			expected: ServiceTargetKind("my-custom-ext"),
		},
		{
			name:      "empty kind returns error",
			kind:      ServiceTargetKind(""),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseServiceHost(tt.kind)
			if tt.expectErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "cannot be empty")
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}

func Test_resourceTypeMismatchError(t *testing.T) {
	err := resourceTypeMismatchError(
		"my-resource",
		"Microsoft.Web/sites",
		azapi.AzureResourceTypeContainerApp,
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "my-resource")
	require.Contains(t, err.Error(), "Microsoft.Web/sites")
	require.Contains(
		t,
		err.Error(),
		string(azapi.AzureResourceTypeContainerApp),
	)
}

func Test_checkResourceType(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		expectedType azapi.AzureResourceType
		expectErr    bool
	}{
		{
			name:         "matching type succeeds",
			resourceType: "Microsoft.Web/sites",
			expectedType: azapi.AzureResourceTypeWebSite,
		},
		{
			name:         "case insensitive match succeeds",
			resourceType: "microsoft.web/sites",
			expectedType: azapi.AzureResourceTypeWebSite,
		},
		{
			name:         "mismatched type fails",
			resourceType: "Microsoft.App/containerApps",
			expectedType: azapi.AzureResourceTypeWebSite,
			expectErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := environment.NewTargetResource(
				"sub-id",
				"rg-name",
				"res-name",
				tt.resourceType,
			)

			err := checkResourceType(resource, tt.expectedType)
			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
