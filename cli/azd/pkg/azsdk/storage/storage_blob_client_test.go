// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockMultiTenantCredentialProvider is a mock implementation for testing
type mockMultiTenantCredentialProvider struct {
	mock.Mock
}

func (m *mockMultiTenantCredentialProvider) GetTokenCredential(
	ctx context.Context, tenantId string) (azcore.TokenCredential, error) {
	args := m.Called(ctx, tenantId)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(azcore.TokenCredential), args.Error(1)
}

// mockSubscriptionTenantResolver is a mock implementation for testing
type mockSubscriptionTenantResolver struct {
	mock.Mock
}

var _ account.SubscriptionTenantResolver = (*mockSubscriptionTenantResolver)(nil)

func (m *mockSubscriptionTenantResolver) LookupTenant(
	ctx context.Context, subscriptionId string) (string, error) {
	args := m.Called(ctx, subscriptionId)
	return args.String(0), args.Error(1)
}

// mockTokenCredential is a minimal mock implementation for testing
type mockTokenCredential struct {
	mock.Mock
}

func (m *mockTokenCredential) GetToken(
	ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	args := m.Called(ctx, opts)
	return args.Get(0).(azcore.AccessToken), args.Error(1)
}

func Test_NewBlobSdkClient_UsesHomeTenantWhenNoSubscriptionId(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionTenantResolver{}
	mockCred := &mockTokenCredential{}

	accountConfig := &AccountConfig{
		AccountName:   "testaccount",
		ContainerName: "testcontainer",
		// No SubscriptionId set - should use home tenant
	}

	coreClientOptions := &azcore.ClientOptions{}

	// Expect credential provider to be called with empty tenant ID (home tenant)
	mockCredProvider.On("GetTokenCredential", mock.Anything, "").Return(mockCred, nil)

	client, err := NewBlobSdkClient(
		mockCredProvider,
		accountConfig,
		coreClientOptions,
		cloud.AzurePublic(),
		mockTenantResolver,
	)

	require.NoError(t, err)
	require.NotNil(t, client)
	mockCredProvider.AssertExpectations(t)

	// TenantResolver should NOT be called when no subscription ID is provided
	mockTenantResolver.AssertNotCalled(t, "LookupTenant", mock.Anything, mock.Anything)
}

func Test_NewBlobSdkClient_ResolvesTenantWhenSubscriptionIdProvided(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionTenantResolver{}
	mockCred := &mockTokenCredential{}

	testSubscriptionId := "test-subscription-id"
	testTenantId := "test-tenant-id"

	accountConfig := &AccountConfig{
		AccountName:    "testaccount",
		ContainerName:  "testcontainer",
		SubscriptionId: testSubscriptionId,
	}

	coreClientOptions := &azcore.ClientOptions{}

	// Expect tenant resolver to be called with the subscription ID
	mockTenantResolver.On("LookupTenant", mock.Anything, testSubscriptionId).Return(testTenantId, nil)

	// Expect credential provider to be called with resolved tenant ID
	mockCredProvider.On("GetTokenCredential", mock.Anything, testTenantId).Return(mockCred, nil)

	client, err := NewBlobSdkClient(
		mockCredProvider,
		accountConfig,
		coreClientOptions,
		cloud.AzurePublic(),
		mockTenantResolver,
	)

	require.NoError(t, err)
	require.NotNil(t, client)
	mockCredProvider.AssertExpectations(t)
	mockTenantResolver.AssertExpectations(t)
}

func Test_NewBlobSdkClient_ReturnsErrorWhenTenantResolutionFails(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionTenantResolver{}

	testSubscriptionId := "test-subscription-id"

	accountConfig := &AccountConfig{
		AccountName:    "testaccount",
		ContainerName:  "testcontainer",
		SubscriptionId: testSubscriptionId,
	}

	coreClientOptions := &azcore.ClientOptions{}

	// Simulate tenant resolution failure
	mockTenantResolver.On("LookupTenant", mock.Anything, testSubscriptionId).
		Return("", errors.New("subscription not found"))

	client, err := NewBlobSdkClient(
		mockCredProvider,
		accountConfig,
		coreClientOptions,
		cloud.AzurePublic(),
		mockTenantResolver,
	)

	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "failed to resolve tenant for subscription")
	mockTenantResolver.AssertExpectations(t)
}
