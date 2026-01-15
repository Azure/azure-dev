// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetResourceTypeDisplayName(t *testing.T) {
	tests := []struct {
		name         string
		resourceType AzureResourceType
		expected     string
	}{
		{
			name:         "AutomationAccount",
			resourceType: AzureResourceTypeAutomationAccount,
			expected:     "Automation account",
		},
		{
			name:         "StorageAccount",
			resourceType: AzureResourceTypeStorageAccount,
			expected:     "Storage account",
		},
		{
			name:         "KeyVault",
			resourceType: AzureResourceTypeKeyVault,
			expected:     "Key Vault",
		},
		{
			name:         "ContainerAppJob",
			resourceType: AzureResourceTypeContainerAppJob,
			expected:     "Container App Job",
		},
		{
			name:         "CosmosDB",
			resourceType: AzureResourceTypeCosmosDb,
			expected:     "Azure Cosmos DB (DocumentDB)",
		},
		{
			name:         "UnknownResourceType",
			resourceType: AzureResourceType("Microsoft.Unknown/unknownResource"),
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetResourceTypeDisplayName(tt.resourceType)
			assert.Equal(t, tt.expected, result)
		})
	}
}
