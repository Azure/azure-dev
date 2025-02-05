// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mockaccount

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/stretchr/testify/mock"
)

type MockSubscriptionService struct {
	mock.Mock
}

func (m *MockSubscriptionService) ListSubscriptions(
	ctx context.Context,
	tenantId string,
) ([]*armsubscriptions.Subscription, error) {
	args := m.Called(ctx, tenantId)
	return args.Get(0).([]*armsubscriptions.Subscription), args.Error(1)
}

func (m *MockSubscriptionService) GetSubscription(
	ctx context.Context,
	subscriptionId string,
	tenantId string,
) (*armsubscriptions.Subscription, error) {
	args := m.Called(ctx, subscriptionId, tenantId)
	return args.Get(0).(*armsubscriptions.Subscription), args.Error(1)
}

func (m *MockSubscriptionService) ListSubscriptionLocations(
	ctx context.Context,
	subscriptionId string,
	tenantId string,
) ([]account.Location, error) {
	args := m.Called(ctx, subscriptionId, tenantId)
	return args.Get(0).([]account.Location), args.Error(1)
}

func (m *MockSubscriptionService) ListTenants(ctx context.Context) ([]armsubscriptions.TenantIDDescription, error) {
	args := m.Called(ctx)
	return args.Get(0).([]armsubscriptions.TenantIDDescription), args.Error(1)
}
