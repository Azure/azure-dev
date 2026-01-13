// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractResourceTypes(t *testing.T) {
	tests := []struct {
		name          string
		template      string
		expectedTypes []string
		expectedError bool
	}{
		{
			name: "SimpleTemplate",
			template: `{
				"resources": [
					{
						"type": "Microsoft.App/containerApps",
						"name": "myapp",
						"location": "eastus"
					},
					{
						"type": "Microsoft.DBforPostgreSQL/flexibleServers",
						"name": "mydb",
						"location": "eastus"
					}
				]
			}`,
			expectedTypes: []string{"Microsoft.App/containerApps", "Microsoft.DBforPostgreSQL/flexibleServers"},
		},
		{
			name: "DuplicateResourceTypes",
			template: `{
				"resources": [
					{
						"type": "Microsoft.App/containerApps",
						"name": "app1"
					},
					{
						"type": "Microsoft.App/containerApps",
						"name": "app2"
					},
					{
						"type": "Microsoft.Storage/storageAccounts",
						"name": "storage1"
					}
				]
			}`,
			expectedTypes: []string{"Microsoft.App/containerApps", "Microsoft.Storage/storageAccounts"},
		},
		{
			name: "EmptyResources",
			template: `{
				"resources": []
			}`,
			expectedTypes: []string{},
		},
		{
			name: "NoResourcesField",
			template: `{
				"parameters": {},
				"outputs": {}
			}`,
			expectedTypes: []string{},
		},
		{
			name: "ResourceWithoutType",
			template: `{
				"resources": [
					{
						"name": "myresource"
					},
					{
						"type": "Microsoft.Web/staticSites",
						"name": "webapp"
					}
				]
			}`,
			expectedTypes: []string{"Microsoft.Web/staticSites"},
		},
		{
			name: "InvalidJSON",
			template: `{
				"resources": [
					{ invalid json
				]
			}`,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawTemplate := RawArmTemplate(tt.template)
			resourceTypes, err := ExtractResourceTypes(rawTemplate)

			if tt.expectedError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Sort both slices for comparison since order doesn't matter
			assert.ElementsMatch(t, tt.expectedTypes, resourceTypes,
				"Expected resource types %v, got %v", tt.expectedTypes, resourceTypes)
		})
	}
}

func TestExtractResourceTypesComplexTemplate(t *testing.T) {
	// Test with a more realistic ARM template structure
	template := map[string]interface{}{
		"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"contentVersion": "1.0.0.0",
		"parameters": map[string]interface{}{
			"location": map[string]interface{}{
				"type": "string",
			},
		},
		"resources": []interface{}{
			map[string]interface{}{
				"type":       "Microsoft.App/containerApps",
				"apiVersion": "2023-05-01",
				"name":       "[parameters('appName')]",
				"location":   "[parameters('location')]",
				"properties": map[string]interface{}{
					"managedEnvironmentId": "[resourceId('Microsoft.App/managedEnvironments', 'env')]",
				},
			},
			map[string]interface{}{
				"type":       "Microsoft.App/managedEnvironments",
				"apiVersion": "2023-05-01",
				"name":       "env",
				"location":   "[parameters('location')]",
			},
			map[string]interface{}{
				"type":       "Microsoft.KeyVault/vaults",
				"apiVersion": "2023-02-01",
				"name":       "keyvault",
				"location":   "[parameters('location')]",
			},
		},
	}

	templateJSON, err := json.Marshal(template)
	require.NoError(t, err)

	rawTemplate := RawArmTemplate(templateJSON)
	resourceTypes, err := ExtractResourceTypes(rawTemplate)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"Microsoft.App/containerApps",
		"Microsoft.App/managedEnvironments",
		"Microsoft.KeyVault/vaults",
	}, resourceTypes)
}
