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
	"github.com/azure/azure-dev/cli/azd/pkg/config"
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

// mockUserConfigManager is a mock implementation for testing
type mockUserConfigManager struct {
	mock.Mock
}

var _ config.UserConfigManager = (*mockUserConfigManager)(nil)

func (m *mockUserConfigManager) Save(c config.Config) error {
	args := m.Called(c)
	return args.Error(0)
}

func (m *mockUserConfigManager) Load() (config.Config, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(config.Config), args.Error(1)
}

func Test_NewBlobSdkClient_UsesHomeTenantWhenNoSubscriptionId(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionTenantResolver{}
	mockCred := &mockTokenCredential{}
	mockConfigMgr := &mockUserConfigManager{}

	accountCfg := &AccountConfig{
		AccountName:   "testaccount",
		ContainerName: "testcontainer",
		// No SubscriptionId set - should use home tenant
	}

	coreClientOptions := &azcore.ClientOptions{}

	// User config has no default subscription either
	mockConfigMgr.On("Load").Return(config.NewEmptyConfig(), nil)

	// Expect credential provider to be called with empty tenant ID (home tenant)
	mockCredProvider.On("GetTokenCredential", mock.Anything, "").Return(mockCred, nil)

	client, err := NewBlobSdkClient(
		mockCredProvider,
		accountCfg,
		mockConfigMgr,
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
	mockConfigMgr := &mockUserConfigManager{}

	testSubscriptionId := "test-subscription-id"
	testTenantId := "test-tenant-id"

	accountCfg := &AccountConfig{
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
		accountCfg,
		mockConfigMgr,
		coreClientOptions,
		cloud.AzurePublic(),
		mockTenantResolver,
	)

	require.NoError(t, err)
	require.NotNil(t, client)
	mockCredProvider.AssertExpectations(t)
	mockTenantResolver.AssertExpectations(t)

	// UserConfigManager should NOT be called when SubscriptionId is already set
	mockConfigMgr.AssertNotCalled(t, "Load")
}

func Test_NewBlobSdkClient_ReturnsErrorWhenTenantResolutionFails(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionTenantResolver{}
	mockConfigMgr := &mockUserConfigManager{}

	testSubscriptionId := "test-subscription-id"

	accountCfg := &AccountConfig{
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
		accountCfg,
		mockConfigMgr,
		coreClientOptions,
		cloud.AzurePublic(),
		mockTenantResolver,
	)

	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "failed to resolve tenant for subscription")
	mockTenantResolver.AssertExpectations(t)
}

func Test_NewBlobSdkClient_FallsBackToDefaultSubscriptionFromUserConfig(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionTenantResolver{}
	mockCred := &mockTokenCredential{}
	mockConfigMgr := &mockUserConfigManager{}

	defaultSubscriptionId := "default-sub-id"
	resolvedTenantId := "resolved-tenant-id"

	accountCfg := &AccountConfig{
		AccountName:   "testaccount",
		ContainerName: "testcontainer",
		// No SubscriptionId - should fall back to user config default
	}

	coreClientOptions := &azcore.ClientOptions{}

	// User config returns a default subscription
	userCfg := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": defaultSubscriptionId,
		},
	})
	mockConfigMgr.On("Load").Return(userCfg, nil)

	// Expect tenant resolver to be called with the default subscription
	mockTenantResolver.On("LookupTenant", mock.Anything, defaultSubscriptionId).Return(resolvedTenantId, nil)

	// Expect credential provider to be called with resolved tenant ID
	mockCredProvider.On("GetTokenCredential", mock.Anything, resolvedTenantId).Return(mockCred, nil)

	client, err := NewBlobSdkClient(
		mockCredProvider,
		accountCfg,
		mockConfigMgr,
		coreClientOptions,
		cloud.AzurePublic(),
		mockTenantResolver,
	)

	require.NoError(t, err)
	require.NotNil(t, client)
	mockConfigMgr.AssertExpectations(t)
	mockTenantResolver.AssertExpectations(t)
	mockCredProvider.AssertExpectations(t)
}

func Test_NewBlobSdkClient_UsesHomeTenantWhenUserConfigLoadFails(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionTenantResolver{}
	mockCred := &mockTokenCredential{}
	mockConfigMgr := &mockUserConfigManager{}

	accountCfg := &AccountConfig{
		AccountName:   "testaccount",
		ContainerName: "testcontainer",
	}

	coreClientOptions := &azcore.ClientOptions{}

	// User config fails to load - should gracefully fall back to home tenant
	mockConfigMgr.On("Load").Return(nil, errors.New("config not found"))

	// Expect credential provider to be called with empty tenant ID (home tenant)
	mockCredProvider.On("GetTokenCredential", mock.Anything, "").Return(mockCred, nil)

	client, err := NewBlobSdkClient(
		mockCredProvider,
		accountCfg,
		mockConfigMgr,
		coreClientOptions,
		cloud.AzurePublic(),
		mockTenantResolver,
	)

	require.NoError(t, err)
	require.NotNil(t, client)
	mockCredProvider.AssertExpectations(t)
	mockTenantResolver.AssertNotCalled(t, "LookupTenant", mock.Anything, mock.Anything)
}
