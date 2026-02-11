// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mockaccount

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/stretchr/testify/mock"
)

type MockSubscriptionManager struct {
	mock.Mock
}

func (m *MockSubscriptionManager) GetSubscriptions(ctx context.Context) ([]account.Subscription, error) {
	args := m.Called(ctx)
	return args.Get(0).([]account.Subscription), args.Error(1)
}

func (m *MockSubscriptionManager) ListLocations(ctx context.Context, subscriptionId string) ([]account.Location, error) {
	args := m.Called(ctx, subscriptionId)
	return args.Get(0).([]account.Location), args.Error(1)
}

func (m *MockSubscriptionManager) GetLocations(ctx context.Context, subscriptionId string) ([]account.Location, error) {
	args := m.Called(ctx, subscriptionId)
	return args.Get(0).([]account.Location), args.Error(1)
}
