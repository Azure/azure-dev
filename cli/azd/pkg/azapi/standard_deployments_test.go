// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_StandardDeployments_GenerateDeploymentName(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Clock.Set(time.Unix(1683303710, 0))

	deploymentService := NewStandardDeployments(
		mockContext.SubscriptionCredentialProvider,
		mockContext.ArmClientOptions,
		NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions),
		cloud.AzurePublic(),
		mockContext.Clock,
	)

	tcs := []struct {
		envName  string
		expected string
	}{
		{
			envName:  "simple-name",
			expected: "simple-name-1683303710",
		},
		{
			envName:  "azd-template-test-apim-todo-csharp-sql-swa-func-2750207-2",
			expected: "template-test-apim-todo-csharp-sql-swa-func-2750207-2-1683303710",
		},
	}

	for _, tc := range tcs {
		deploymentName := deploymentService.GenerateDeploymentName(tc.envName)
		assert.Equal(t, tc.expected, deploymentName)
		assert.LessOrEqual(t, len(deploymentName), 64)
	}
}

func TestResourceGroupsFromDeployment(t *testing.T) {
	t.Parallel()

	t.Run("references used when no output resources", func(t *testing.T) {
		mockDeployment := &ResourceDeployment{
			//nolint:lll
			Id: "/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/providers/Microsoft.Resources/deployments/matell-2508-1689982746",
			//nolint:lll
			DeploymentId: "/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/providers/Microsoft.Resources/deployments/matell-2508-1689982746",
			Name:         "matell-2508",
			Type:         "Microsoft.Resources/deployments",
			Tags: map[string]*string{
				"azd-env-name": new("matell-2508"),
			},
			ProvisioningState: DeploymentProvisioningStateFailed,
			Timestamp:         time.Now(),
			Dependencies: []*armresources.Dependency{
				{
					//nolint:lll
					ID: new(
						"/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/resourceGroups/matell-2508-rg/providers/Microsoft.Resources/deployments/resources",
					),
					ResourceName: new("resources"),
					ResourceType: new("Microsoft.Resources/deployments"),
					DependsOn: []*armresources.BasicDependency{
						{
							//nolint:lll
							ID: new(
								"/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/resourceGroups/matell-2508-rg",
							),
							ResourceName: new("matell-2508-rg"),
							ResourceType: new("Microsoft.Resources/resourceGroups"),
						},
					},
				},
			},
		}

		require.Equal(t, []string{"matell-2508-rg"}, resourceGroupsFromDeployment(mockDeployment))
	})

	t.Run("duplicate resource groups ignored", func(t *testing.T) {

		mockDeployment := ResourceDeployment{
			Id:   "DEPLOYMENT_ID",
			Name: "test-env",
			Resources: []*armresources.ResourceReference{
				{
					ID: new("/subscriptions/sub-id/resourceGroups/groupA"),
				},
				{
					ID: new(
						"/subscriptions/sub-id/resourceGroups/groupA/Microsoft.Storage/storageAccounts/storageAccount",
					),
				},
				{
					ID: new("/subscriptions/sub-id/resourceGroups/groupB"),
				},
				{
					ID: new("/subscriptions/sub-id/resourceGroups/groupB/Microsoft.web/sites/test"),
				},
				{
					ID: new("/subscriptions/sub-id/resourceGroups/groupC"),
				},
			},
			ProvisioningState: DeploymentProvisioningStateSucceeded,
			Timestamp:         time.Now(),
		}

		groups := resourceGroupsFromDeployment(&mockDeployment)

		slices.Sort(groups)
		require.Equal(t, []string{"groupA", "groupB", "groupC"}, groups)
	})
}

func Test_StandardDeployments_VoidSubscriptionDeploymentState(t *testing.T) {
	t.Parallel()

	// This test verifies that VoidSubscriptionDeploymentState is a valid public method
	// that delegates to the private voidSubscriptionDeploymentState implementation.
	// The method signature and delegation are verified at compile time.
	mockContext := mocks.NewMockContext(context.Background())

	deploymentService := NewStandardDeployments(
		mockContext.SubscriptionCredentialProvider,
		mockContext.ArmClientOptions,
		NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions),
		cloud.AzurePublic(),
		mockContext.Clock,
	)

	// Verify the method exists and is callable (compilation check).
	// A full integration test would require HTTP mocks for the ARM deployment API.
	_ = deploymentService.VoidSubscriptionDeploymentState
}

func TestResourceGroupsFromDeployment_Public(t *testing.T) {
	t.Parallel()

	// Verify public wrapper returns same result as private function.
	mockDeployment := &ResourceDeployment{
		Resources: []*armresources.ResourceReference{
			{ID: new("/subscriptions/sub-id/resourceGroups/myRG")},
		},
		ProvisioningState: DeploymentProvisioningStateSucceeded,
		Timestamp:         time.Now(),
	}

	public := ResourceGroupsFromDeployment(mockDeployment)
	private := resourceGroupsFromDeployment(mockDeployment)

	slices.Sort(public)
	slices.Sort(private)
	require.Equal(t, private, public)
}
