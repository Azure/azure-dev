// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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
