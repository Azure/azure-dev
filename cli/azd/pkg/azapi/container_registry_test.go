// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazsdk"
	"github.com/stretchr/testify/require"
)

func Test_FindContainerRegistryResourceGroup(t *testing.T) {
	t.Run("ResolvesDifferentResourceGroup", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		mockazsdk.MockContainerRegistryList(mockContext, []*armcontainerregistry.Registry{
			{
				Name: to.Ptr("myregistry"),
				ID: to.Ptr(
					"/subscriptions/sub1/resourceGroups/shared-rg/providers/" +
						"Microsoft.ContainerRegistry/registries/myregistry",
				),
			},
		})

		svc := NewContainerRegistryService(
			mockaccount.SubscriptionCredentialProviderFunc(
				func(_ context.Context, _ string) (azcore.TokenCredential, error) {
					return mockContext.Credentials, nil
				}),
			nil,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)

		rg, err := svc.FindContainerRegistryResourceGroup(*mockContext.Context, "sub1", "myregistry")
		require.NoError(t, err)
		require.Equal(t, "shared-rg", rg)
	})

	t.Run("RegistryNotFound", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		mockazsdk.MockContainerRegistryList(mockContext, []*armcontainerregistry.Registry{})

		svc := NewContainerRegistryService(
			mockaccount.SubscriptionCredentialProviderFunc(
				func(_ context.Context, _ string) (azcore.TokenCredential, error) {
					return mockContext.Credentials, nil
				}),
			nil,
			mockContext.ArmClientOptions,
			mockContext.CoreClientOptions,
		)

		_, err := svc.FindContainerRegistryResourceGroup(*mockContext.Context, "sub1", "nonexistent")
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot find registry with name 'nonexistent'")
	})
}
