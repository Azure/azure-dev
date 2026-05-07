// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package devcentersdk

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResourceId(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectError    bool
		subscriptionId string
		resourceGroup  string
		provider       string
		resourcePath   string
		resourceName   string
	}{
		{
			name: "valid resource ID",
			input: "/subscriptions/sub-123/resourceGroups/rg-test" +
				"/providers/Microsoft.DevCenter" +
				"/devcenters/my-devcenter",
			subscriptionId: "sub-123",
			resourceGroup:  "rg-test",
			provider:       "Microsoft.DevCenter",
			resourcePath:   "devcenters",
			resourceName:   "my-devcenter",
		},
		{
			name: "GUID subscription",
			input: "/subscriptions" +
				"/00000000-0000-0000-0000-000000000000" +
				"/resourceGroups/my-rg" +
				"/providers/Microsoft.Web/sites/my-app",
			subscriptionId: "00000000-0000-0000-0000-000000000000",
			resourceGroup:  "my-rg",
			provider:       "Microsoft.Web",
			resourcePath:   "sites",
			resourceName:   "my-app",
		},
		{
			name:        "empty string",
			input:       "",
			expectError: true,
		},
		{
			name:        "invalid format",
			input:       "not-a-resource-id",
			expectError: true,
		},
		{
			name: "missing resource name",
			input: "/subscriptions/sub-123" +
				"/resourceGroups/rg-test" +
				"/providers/Microsoft.DevCenter",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := NewResourceId(tt.input)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.input, result.Id)
			assert.Equal(
				t, tt.subscriptionId, result.SubscriptionId,
			)
			assert.Equal(t, tt.resourceGroup, result.ResourceGroup)
			assert.Equal(t, tt.provider, result.Provider)
			assert.Equal(t, tt.resourcePath, result.ResourcePath)
			assert.Equal(t, tt.resourceName, result.ResourceName)
		})
	}
}

func TestNewResourceGroupId(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectError    bool
		subscriptionId string
		rgName         string
	}{
		{
			name: "valid resource group ID",
			input: "/subscriptions/sub-123" +
				"/resourceGroups/my-rg",
			subscriptionId: "sub-123",
			rgName:         "my-rg",
		},
		{
			name: "GUID subscription",
			input: "/subscriptions" +
				"/00000000-0000-0000-0000-000000000000" +
				"/resourceGroups/production-rg",
			subscriptionId: "00000000-0000-0000-0000-000000000000",
			rgName:         "production-rg",
		},
		{
			name:        "empty string",
			input:       "",
			expectError: true,
		},
		{
			name:        "invalid format",
			input:       "not-a-resource-group-id",
			expectError: true,
		},
		{
			name:        "subscription only",
			input:       "/subscriptions/sub-123",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := NewResourceGroupId(tt.input)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.input, result.Id)
			assert.Equal(
				t, tt.subscriptionId, result.SubscriptionId,
			)
			assert.Equal(t, tt.rgName, result.Name)
		})
	}
}

func TestNewApiVersionPolicy(t *testing.T) {
	t.Run("nil version uses default", func(t *testing.T) {
		policy := NewApiVersionPolicy(nil)
		require.NotNil(t, policy)
	})

	t.Run("custom version", func(t *testing.T) {
		version := "2023-10-01"
		policy := NewApiVersionPolicy(&version)
		require.NotNil(t, policy)
	})
}

func TestParameterTypes(t *testing.T) {
	assert.Equal(
		t, ParameterType("string"), ParameterTypeString,
	)
	assert.Equal(t, ParameterType("int"), ParameterTypeInt)
	assert.Equal(t, ParameterType("bool"), ParameterTypeBool)
}

func TestProvisioningStates(t *testing.T) {
	assert.Equal(
		t, ProvisioningState("Succeeded"),
		ProvisioningStateSucceeded,
	)
	assert.Equal(
		t, ProvisioningState("Creating"),
		ProvisioningStateCreating,
	)
	assert.Equal(
		t, ProvisioningState("Deleting"),
		ProvisioningStateDeleting,
	)
}

func TestOutputParameterTypes(t *testing.T) {
	assert.Equal(
		t, OutputParameterType("array"), OutputParameterTypeArray,
	)
	assert.Equal(
		t, OutputParameterType("boolean"),
		OutputParameterTypeBoolean,
	)
	assert.Equal(
		t, OutputParameterType("number"),
		OutputParameterTypeNumber,
	)
	assert.Equal(
		t, OutputParameterType("object"),
		OutputParameterTypeObject,
	)
	assert.Equal(
		t, OutputParameterType("string"),
		OutputParameterTypeString,
	)
}

func TestServiceConfig(t *testing.T) {
	assert.Equal(
		t,
		"https://management.core.windows.net",
		ServiceConfig.Audience,
	)
}
