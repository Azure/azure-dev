// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mockazapi

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/stretchr/testify/mock"
)

type MockResourceService struct {
	mock.Mock
}

func (m *MockResourceService) ListResourceGroup(
	ctx context.Context,
	subscriptionId string,
	listOptions *azapi.ListResourceGroupOptions,
) ([]*azapi.Resource, error) {
	args := m.Called(ctx, subscriptionId, listOptions)
	return args.Get(0).([]*azapi.Resource), args.Error(1)
}

func (m *MockResourceService) ListResourceGroupResources(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	listOptions *azapi.ListResourceGroupResourcesOptions,
) ([]*azapi.ResourceExtended, error) {
	args := m.Called(ctx, subscriptionId, resourceGroupName, listOptions)
	return args.Get(0).([]*azapi.ResourceExtended), args.Error(1)
}

func (m *MockResourceService) ListSubscriptionResources(
	ctx context.Context,
	subscriptionId string,
	listOptions *armresources.ClientListOptions,
) ([]*azapi.ResourceExtended, error) {
	args := m.Called(ctx, subscriptionId, listOptions)
	return args.Get(0).([]*azapi.ResourceExtended), args.Error(1)
}

func (m *MockResourceService) CreateOrUpdateResourceGroup(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	location string,
	tags map[string]*string,
) (*azapi.ResourceGroup, error) {
	args := m.Called(ctx, subscriptionId, resourceGroupName, location, tags)
	return args.Get(0).(*azapi.ResourceGroup), args.Error(1)
}

func (m *MockResourceService) GetResource(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
	apiVersion string,
) (azapi.ResourceExtended, error) {
	args := m.Called(ctx, subscriptionId, resourceId, apiVersion)
	return args.Get(0).(azapi.ResourceExtended), args.Error(1)
}

func (m *MockResourceService) DeleteResource(ctx context.Context, subscriptionId string, resourceId string) error {
	args := m.Called(ctx, subscriptionId, resourceId)
	return args.Error(0)
}
