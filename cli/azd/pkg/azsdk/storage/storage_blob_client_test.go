// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
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

// mockSubscriptionResolver is a mock implementation for testing.
type mockSubscriptionResolver struct {
	mock.Mock
}

var _ account.SubscriptionResolver = (*mockSubscriptionResolver)(nil)

func (m *mockSubscriptionResolver) GetSubscription(
	ctx context.Context, subscriptionId string,
) (*account.Subscription, error) {
	args := m.Called(ctx, subscriptionId)

	if subscription, ok := args.Get(0).(*account.Subscription); ok {
		return subscription, args.Error(1)
	}

	if tenantId, ok := args.Get(0).(string); ok {
		return &account.Subscription{
			Id:                 subscriptionId,
			TenantId:           "resource-" + tenantId,
			UserAccessTenantId: tenantId,
		}, args.Error(1)
	}

	return nil, args.Error(1)
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
	mockTenantResolver := &mockSubscriptionResolver{}
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

	// GetSubscription should NOT be called when no subscription ID is provided.
	mockTenantResolver.AssertNotCalled(t, "GetSubscription", mock.Anything, mock.Anything)
}

func Test_NewBlobSdkClient_ResolvesTenantWhenSubscriptionIdProvided(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionResolver{}
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

	// Expect the subscription resolver to be called with the subscription ID.
	mockTenantResolver.On("GetSubscription", mock.Anything, testSubscriptionId).
		Return(&account.Subscription{
			Id:                 testSubscriptionId,
			TenantId:           "resource-" + testTenantId,
			UserAccessTenantId: testTenantId,
		}, nil)

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
	mockTenantResolver := &mockSubscriptionResolver{}
	mockConfigMgr := &mockUserConfigManager{}

	testSubscriptionId := "test-subscription-id"

	accountCfg := &AccountConfig{
		AccountName:    "testaccount",
		ContainerName:  "testcontainer",
		SubscriptionId: testSubscriptionId,
	}

	coreClientOptions := &azcore.ClientOptions{}

	// Simulate subscription resolution failure.
	mockTenantResolver.On("GetSubscription", mock.Anything, testSubscriptionId).
		Return((*account.Subscription)(nil), errors.New("subscription not found"))

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
	require.Contains(t, err.Error(), "failed to get subscription")
	mockTenantResolver.AssertExpectations(t)
}

func Test_NewBlobSdkClient_FallsBackToDefaultSubscriptionFromUserConfig(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionResolver{}
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

	// Expect the subscription resolver to be called with the default subscription.
	mockTenantResolver.On("GetSubscription", mock.Anything, defaultSubscriptionId).
		Return(&account.Subscription{
			Id:                 defaultSubscriptionId,
			TenantId:           "resource-" + resolvedTenantId,
			UserAccessTenantId: resolvedTenantId,
		}, nil)

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
	mockTenantResolver := &mockSubscriptionResolver{}
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
	mockTenantResolver.AssertNotCalled(t, "GetSubscription", mock.Anything, mock.Anything)
}

func Test_NewBlobSdkClient_UsesCustomEndpoint(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionResolver{}
	mockCred := &mockTokenCredential{}
	mockConfigMgr := &mockUserConfigManager{}

	accountCfg := &AccountConfig{
		AccountName:   "myaccount",
		ContainerName: "mycontainer",
		Endpoint:      "blob.core.usgovcloudapi.net",
	}

	coreClientOptions := &azcore.ClientOptions{}

	mockConfigMgr.On("Load").Return(config.NewEmptyConfig(), nil)
	mockCredProvider.On(
		"GetTokenCredential", mock.Anything, "",
	).Return(mockCred, nil)

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
	// Custom endpoint should NOT be overwritten
	require.Equal(t, "blob.core.usgovcloudapi.net", accountCfg.Endpoint)
	mockCredProvider.AssertExpectations(t)
}

func Test_NewBlobSdkClient_DefaultEndpointFromCloud(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionResolver{}
	mockCred := &mockTokenCredential{}
	mockConfigMgr := &mockUserConfigManager{}

	accountCfg := &AccountConfig{
		AccountName:   "myaccount",
		ContainerName: "mycontainer",
		// Endpoint empty — should be populated from cloud
	}

	coreClientOptions := &azcore.ClientOptions{}

	mockConfigMgr.On("Load").Return(config.NewEmptyConfig(), nil)
	mockCredProvider.On(
		"GetTokenCredential", mock.Anything, "",
	).Return(mockCred, nil)

	azureCloud := cloud.AzurePublic()

	client, err := NewBlobSdkClient(
		mockCredProvider,
		accountCfg,
		mockConfigMgr,
		coreClientOptions,
		azureCloud,
		mockTenantResolver,
	)

	require.NoError(t, err)
	require.NotNil(t, client)
	require.Equal(t, azureCloud.StorageEndpointSuffix, accountCfg.Endpoint)
}

func Test_NewBlobSdkClient_CredentialProviderError(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionResolver{}
	mockConfigMgr := &mockUserConfigManager{}

	accountCfg := &AccountConfig{
		AccountName:   "myaccount",
		ContainerName: "mycontainer",
	}

	coreClientOptions := &azcore.ClientOptions{}

	mockConfigMgr.On("Load").Return(config.NewEmptyConfig(), nil)
	mockCredProvider.On(
		"GetTokenCredential", mock.Anything, "",
	).Return(nil, errors.New("credential unavailable"))

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
	require.Contains(t, err.Error(), "credential unavailable")
}

func Test_NewBlobSdkClient_EmptyDefaultSubscriptionIgnored(t *testing.T) {
	mockCredProvider := &mockMultiTenantCredentialProvider{}
	mockTenantResolver := &mockSubscriptionResolver{}
	mockCred := &mockTokenCredential{}
	mockConfigMgr := &mockUserConfigManager{}

	accountCfg := &AccountConfig{
		AccountName:   "myaccount",
		ContainerName: "mycontainer",
	}

	coreClientOptions := &azcore.ClientOptions{}

	// User config has empty default subscription
	userCfg := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": "",
		},
	})
	mockConfigMgr.On("Load").Return(userCfg, nil)
	mockCredProvider.On(
		"GetTokenCredential", mock.Anything, "",
	).Return(mockCred, nil)

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
	// Tenant resolver should NOT be called for empty subscription
	mockTenantResolver.AssertNotCalled(
		t, "LookupTenant", mock.Anything, mock.Anything,
	)
}

func Test_NewBlobClient_ReturnsValidClient(t *testing.T) {
	cfg := &AccountConfig{
		AccountName:   "testaccount",
		ContainerName: "testcontainer",
		Endpoint:      "blob.core.windows.net",
	}

	// NewBlobClient only wraps config+client; it doesn't
	// call Azure, so a nil azblob.Client is fine for the
	// factory test (we only check the interface is returned).
	bc := NewBlobClient(cfg, nil)
	require.NotNil(t, bc)
}

func Test_AccountConfig_Fields(t *testing.T) {
	cfg := AccountConfig{
		AccountName:    "sa",
		ContainerName:  "cn",
		Endpoint:       "ep",
		SubscriptionId: "sid",
	}

	require.Equal(t, "sa", cfg.AccountName)
	require.Equal(t, "cn", cfg.ContainerName)
	require.Equal(t, "ep", cfg.Endpoint)
	require.Equal(t, "sid", cfg.SubscriptionId)
}

func Test_ErrContainerNotFound(t *testing.T) {
	require.NotNil(t, ErrContainerNotFound)
	require.Equal(t, "container not found", ErrContainerNotFound.Error())
}
