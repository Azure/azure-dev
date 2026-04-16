// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultiTenantCredentialProvider_GetTokenCredential(t *testing.T) {
	t.Run("CachesCredential", func(t *testing.T) {
		m := &Manager{
			cloud: cloud.AzurePublic(),
		}

		provider := NewMultiTenantCredentialProvider(m)
		mtp := provider.(*multiTenantCredentialProvider)

		// Pre-populate cache
		fakeCred := &fakeTokenCredential{
			token: azcore.AccessToken{
				Token:     "cached-token",
				ExpiresOn: time.Now().Add(time.Hour),
			},
		}
		mtp.tenantCredentials.Store("cached-tenant", fakeCred)

		// Should return cached credential
		cred, err := provider.GetTokenCredential(
			t.Context(), "cached-tenant",
		)
		require.NoError(t, err)
		assert.Equal(t, fakeCred, cred)
	})

	t.Run("ErrorFromCredentialForCurrentUser", func(t *testing.T) {
		m := &Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: newMemoryUserConfigManager(),
			publicClient:      &mockPublicClient{},
		}

		provider := NewMultiTenantCredentialProvider(m)
		_, err := provider.GetTokenCredential(
			t.Context(), "some-tenant",
		)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoCurrentUser)
	})
}

// --- ClaimsForCurrentUser ---

func TestClaimsForCurrentUser_NotLoggedIn(t *testing.T) {
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient:      &mockPublicClient{},
	}

	_, err := m.ClaimsForCurrentUser(t.Context(), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoCurrentUser)
}
