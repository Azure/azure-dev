// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/pkg/azapi"
	"github.com/stretchr/testify/require"
)

func TestDeploymentHost(t *testing.T) {
	tests := []struct {
		name             string
		deploymentResult *azapi.ResourceDeployment
		expectedHostType azapi.AzureResourceType
		expectedName     string
		expectError      bool
		expectedErrorMsg string
	}{
		{
			name: "ContainerApp deployment",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{
					{
						ID: to.Ptr("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.App/containerApps/my-container-app"),
					},
				},
			},
			expectedHostType: azapi.AzureResourceTypeContainerApp,
			expectedName:     "my-container-app",
			expectError:      false,
		},
		{
			name: "ContainerAppJob deployment",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{
					{
						ID: to.Ptr("/subscriptions/sub-id/resourceGroups/rg-name/providers/Microsoft.App/jobs/my-job"),
					},
				},
			},
			expectedHostType: azapi.AzureResourceTypeContainerAppJob,
			expectedName:     "my-job",
			expectError:      false,
		},
		{
			name: "WebSite deployment",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{
					{
						ID: to.Ptr("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.Web/sites/my-web-app"),
					},
				},
			},
			expectedHostType: azapi.AzureResourceTypeWebSite,
			expectedName:     "my-web-app",
			expectError:      false,
		},
		{
			name: "Unknown resource type",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{
					{
						ID: to.Ptr("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.Storage/storageAccounts/my-storage"),
					},
				},
			},
			expectError:      true,
			expectedErrorMsg: "didn't find any known application host from the deployment",
		},
		{
			name: "No resources in deployment",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{},
			},
			expectError:      true,
			expectedErrorMsg: "didn't find any known application host from the deployment",
		},
		{
			name:             "Nil deployment result",
			deploymentResult: nil,
			expectError:      true,
			expectedErrorMsg: "deployment result is empty",
		},
		{
			name: "Multiple resources with container app",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{
					{
						ID: to.Ptr("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.Storage/storageAccounts/my-storage"),
					},
					{
						ID: to.Ptr("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.App/containerApps/my-container-app"),
					},
				},
			},
			expectedHostType: azapi.AzureResourceTypeContainerApp,
			expectedName:     "my-container-app",
			expectError:      false,
		},
		{
			name: "Multiple resources with container app job",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{
					{
						ID: to.Ptr("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.Storage/storageAccounts/my-storage"),
					},
					{
						ID: to.Ptr("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.App/jobs/my-processor-job"),
					},
				},
			},
			expectedHostType: azapi.AzureResourceTypeContainerAppJob,
			expectedName:     "my-processor-job",
			expectError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := deploymentHost(tt.deploymentResult)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedErrorMsg)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedHostType, result.hostType)
				require.Equal(t, tt.expectedName, result.name)
			}
		})
	}
}
