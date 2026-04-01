// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mode / SetBuiltInAuthMode ---

func TestMode(t *testing.T) {
	t.Run("ExternalAuth", func(t *testing.T) {
		m := Manager{
			userConfigManager: newMemoryUserConfigManager(),
			externalAuthCfg: ExternalAuthConfiguration{
				Endpoint: "https://example.com",
				Key:      "secret-key",
			},
		}
		mode, err := m.Mode()
		require.NoError(t, err)
		assert.Equal(t, ExternalRequest, mode)
	})

	t.Run("AzDelegated", func(t *testing.T) {
		ucm := newMemoryUserConfigManager()
		cfg, err := ucm.Load()
		require.NoError(t, err)
		require.NoError(t, cfg.Set(useAzCliAuthKey, "true"))
		require.NoError(t, ucm.Save(cfg))

		m := Manager{
			userConfigManager: ucm,
		}
		mode, err := m.Mode()
		require.NoError(t, err)
		assert.Equal(t, AzDelegated, mode)
	})

	t.Run("AzdBuiltIn", func(t *testing.T) {
		m := Manager{
			userConfigManager: newMemoryUserConfigManager(),
		}
		mode, err := m.Mode()
		require.NoError(t, err)
		assert.Equal(t, AzdBuiltIn, mode)
	})
}

func TestSetBuiltInAuthMode(t *testing.T) {
	t.Run("AlreadyBuiltIn", func(t *testing.T) {
		m := Manager{
			userConfigManager: newMemoryUserConfigManager(),
		}
		err := m.SetBuiltInAuthMode()
		require.NoError(t, err)
		// Should be a no-op
		mode, err := m.Mode()
		require.NoError(t, err)
		assert.Equal(t, AzdBuiltIn, mode)
	})

	t.Run("TransitionFromAzDelegated", func(t *testing.T) {
		ucm := newMemoryUserConfigManager()
		cfg, err := ucm.Load()
		require.NoError(t, err)
		require.NoError(t, cfg.Set(useAzCliAuthKey, "true"))
		require.NoError(t, ucm.Save(cfg))

		m := Manager{
			userConfigManager: ucm,
		}
		// Verify it's currently delegated
		mode, err := m.Mode()
		require.NoError(t, err)
		assert.Equal(t, AzDelegated, mode)

		// Transition
		err = m.SetBuiltInAuthMode()
		require.NoError(t, err)

		// Verify it's now built-in
		mode, err = m.Mode()
		require.NoError(t, err)
		assert.Equal(t, AzdBuiltIn, mode)
	})

	t.Run("ErrorWhenExternalAuth", func(t *testing.T) {
		m := Manager{
			userConfigManager: newMemoryUserConfigManager(),
			externalAuthCfg: ExternalAuthConfiguration{
				Endpoint: "https://example.com",
				Key:      "secret-key",
			},
		}
		err := m.SetBuiltInAuthMode()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot change auth mode")
	})
}

// --- GetLoggedInServicePrincipalTenantID ---

func TestGetLoggedInServicePrincipalTenantID(t *testing.T) {
	t.Run("ExternalAuth", func(t *testing.T) {
		m := Manager{
			externalAuthCfg: ExternalAuthConfiguration{
				Endpoint: "https://example.com",
				Key:      "secret-key",
			},
		}
		tenantID, err := m.GetLoggedInServicePrincipalTenantID(t.Context())
		require.NoError(t, err)
		assert.Nil(t, tenantID)
	})

	t.Run("LegacyAuth", func(t *testing.T) {
		ucm := newMemoryUserConfigManager()
		cfg, err := ucm.Load()
		require.NoError(t, err)
		require.NoError(t, cfg.Set(useAzCliAuthKey, "true"))
		require.NoError(t, ucm.Save(cfg))

		m := Manager{
			userConfigManager: ucm,
		}
		tenantID, err := m.GetLoggedInServicePrincipalTenantID(t.Context())
		require.NoError(t, err)
		assert.Nil(t, tenantID)
	})

	t.Run("ServicePrincipalReturns_TenantID", func(t *testing.T) {
		credCache := &memoryCache{cache: make(map[string][]byte)}
		m := Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: newMemoryUserConfigManager(),
			credentialCache:   credCache,
			cloud:             cloud.AzurePublic(),
			publicClient:      &mockPublicClient{},
		}

		_, err := m.LoginWithServicePrincipalSecret(
			t.Context(), "testTenantId", "testClientId", "testSecret",
		)
		require.NoError(t, err)

		tenantID, err := m.GetLoggedInServicePrincipalTenantID(t.Context())
		require.NoError(t, err)
		require.NotNil(t, tenantID)
		assert.Equal(t, "testTenantId", *tenantID)
	})

	t.Run("UserAccountReturns_Nil", func(t *testing.T) {
		m := &Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: newMemoryUserConfigManager(),
			publicClient:      &mockPublicClient{},
			cloud:             cloud.AzurePublic(),
		}

		// Login interactively (creates a user/HomeAccountID entry)
		_, err := m.LoginInteractive(t.Context(), nil, "", nil)
		require.NoError(t, err)

		tenantID, err := m.GetLoggedInServicePrincipalTenantID(t.Context())
		require.NoError(t, err)
		assert.Nil(t, tenantID)
	})

	t.Run("NoCurrentUser", func(t *testing.T) {
		m := Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: newMemoryUserConfigManager(),
			publicClient:      &mockPublicClient{},
		}

		_, err := m.GetLoggedInServicePrincipalTenantID(t.Context())
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoCurrentUser)
	})
}

// --- CredentialForCurrentUser branch matrix ---

func TestCredentialForCurrentUser_ExternalAuth(t *testing.T) {
	m := Manager{
		externalAuthCfg: ExternalAuthConfiguration{
			Endpoint: "https://example.com",
			Key:      "secret-key",
		},
	}

	cred, err := m.CredentialForCurrentUser(t.Context(), nil)
	require.NoError(t, err)
	require.IsType(t, new(RemoteCredential), cred)
}

func TestCredentialForCurrentUser_ExternalAuthWithTenantID(t *testing.T) {
	m := Manager{
		externalAuthCfg: ExternalAuthConfiguration{
			Endpoint: "https://example.com",
			Key:      "secret-key",
		},
	}

	cred, err := m.CredentialForCurrentUser(t.Context(), &CredentialForCurrentUserOptions{
		TenantID: "custom-tenant",
	})
	require.NoError(t, err)
	require.IsType(t, new(RemoteCredential), cred)
	rc := cred.(*RemoteCredential)
	assert.Equal(t, "custom-tenant", rc.tenantID)
}

func TestCredentialForCurrentUser_LegacyAuth_Error(t *testing.T) {
	ucm := newMemoryUserConfigManager()
	cfg, err := ucm.Load()
	require.NoError(t, err)
	require.NoError(t, cfg.Set(useAzCliAuthKey, "true"))
	require.NoError(t, ucm.Save(cfg))

	m := Manager{
		userConfigManager: ucm,
	}

	// With default options (nil)
	cred, err := m.CredentialForCurrentUser(t.Context(), nil)
	require.NoError(t, err)
	require.IsType(t, new(azidentity.AzureCLICredential), cred)
}

func TestCredentialForCurrentUser_LegacyAuthWithTenant(t *testing.T) {
	ucm := newMemoryUserConfigManager()
	cfg, err := ucm.Load()
	require.NoError(t, err)
	require.NoError(t, cfg.Set(useAzCliAuthKey, "true"))
	require.NoError(t, ucm.Save(cfg))

	m := Manager{
		userConfigManager: ucm,
	}

	cred, err := m.CredentialForCurrentUser(t.Context(), &CredentialForCurrentUserOptions{
		TenantID: "my-tenant",
	})
	require.NoError(t, err)
	require.IsType(t, new(azidentity.AzureCLICredential), cred)
}

func TestCredentialForCurrentUser_ManagedIdentityNoClientID(t *testing.T) {
	m := Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient:      &mockPublicClient{},
		cloud:             cloud.AzurePublic(),
	}

	// Manually save managed identity login
	err := m.saveUserProperties(&userProperties{ManagedIdentity: true})
	require.NoError(t, err)

	cred, err := m.CredentialForCurrentUser(t.Context(), nil)
	require.NoError(t, err)
	require.IsType(t, new(azidentity.ManagedIdentityCredential), cred)
}

func TestCredentialForCurrentUser_ManagedIdentityWithClientID(t *testing.T) {
	m := Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient:      &mockPublicClient{},
		cloud:             cloud.AzurePublic(),
	}

	// Manually save managed identity with client ID
	clientID := "my-client-id"
	err := m.saveUserProperties(&userProperties{
		ManagedIdentity: true,
		ClientID:        &clientID,
	})
	require.NoError(t, err)

	cred, err := m.CredentialForCurrentUser(t.Context(), nil)
	require.NoError(t, err)
	require.IsType(t, new(azidentity.ManagedIdentityCredential), cred)
}

func TestCredentialForCurrentUser_SPWithTenantOverride(t *testing.T) {
	credCache := &memoryCache{cache: make(map[string][]byte)}
	m := Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		credentialCache:   credCache,
		cloud:             cloud.AzurePublic(),
		publicClient:      &mockPublicClient{},
	}

	_, err := m.LoginWithServicePrincipalSecret(
		t.Context(), "origTenant", "testClient", "testSecret",
	)
	require.NoError(t, err)

	// Request with a different tenant
	cred, err := m.CredentialForCurrentUser(t.Context(), &CredentialForCurrentUserOptions{
		TenantID: "overrideTenant",
	})
	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientSecretCredential), cred)
}

func TestCredentialForCurrentUser_UserAccountWithTenantID(t *testing.T) {
	m := &Manager{
		configManager:       newMemoryConfigManager(),
		userConfigManager:   newMemoryUserConfigManager(),
		publicClient:        &mockPublicClient{},
		cloud:               cloud.AzurePublic(),
		publicClientOptions: []public.Option{},
	}

	// Login interactively first (sets HomeAccountID)
	_, err := m.LoginInteractive(t.Context(), nil, "", nil)
	require.NoError(t, err)

	// Now request credential with a tenant ID override
	cred, err := m.CredentialForCurrentUser(t.Context(), &CredentialForCurrentUserOptions{
		TenantID: "cross-tenant-id",
	})
	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)
}

func TestCredentialForCurrentUser_UserAccountWithoutTenantID(t *testing.T) {
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient:      &mockPublicClient{},
		cloud:             cloud.AzurePublic(),
	}

	// Login interactively first
	_, err := m.LoginInteractive(t.Context(), nil, "", nil)
	require.NoError(t, err)

	// No tenant override
	cred, err := m.CredentialForCurrentUser(t.Context(), nil)
	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)
}

func TestCredentialForCurrentUser_HomeAccountNotInAccounts(t *testing.T) {
	// A publicClient that returns no matching accounts
	emptyAccounts := &mockPublicClientWithAccounts{
		accounts: []public.Account{
			{HomeAccountID: "other-account-id"},
		},
	}

	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient:      emptyAccounts,
		cloud:             cloud.AzurePublic(),
	}

	// Save a user with a HomeAccountID that doesn't match any account
	err := m.saveUserProperties(&userProperties{
		HomeAccountID: new("nonexistent-account-id"),
	})
	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(t.Context(), nil)
	require.ErrorIs(t, err, ErrNoCurrentUser)
}

// --- LoginWithManagedIdentity ---

func TestLoginWithManagedIdentity(t *testing.T) {
	t.Run("NoClientID", func(t *testing.T) {
		m := Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: newMemoryUserConfigManager(),
			cloud:             cloud.AzurePublic(),
			publicClient:      &mockPublicClient{},
		}

		cred, err := m.LoginWithManagedIdentity(t.Context(), "")
		require.NoError(t, err)
		require.IsType(t, new(azidentity.ManagedIdentityCredential), cred)

		// Verify roundtrip
		cred2, err := m.CredentialForCurrentUser(t.Context(), nil)
		require.NoError(t, err)
		require.IsType(t, new(azidentity.ManagedIdentityCredential), cred2)
	})

	t.Run("WithClientID", func(t *testing.T) {
		m := Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: newMemoryUserConfigManager(),
			cloud:             cloud.AzurePublic(),
			publicClient:      &mockPublicClient{},
		}

		cred, err := m.LoginWithManagedIdentity(t.Context(), "my-client-id")
		require.NoError(t, err)
		require.IsType(t, new(azidentity.ManagedIdentityCredential), cred)
	})
}

// --- LoginWithAzurePipelinesFederatedTokenProvider ---

func TestLoginWithAzurePipelinesFederatedTokenProvider(t *testing.T) {
	t.Run("MissingSystemAccessToken", func(t *testing.T) {
		m := Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: newMemoryUserConfigManager(),
			cloud:             cloud.AzurePublic(),
		}

		_, err := m.LoginWithAzurePipelinesFederatedTokenProvider(
			t.Context(), "tenant", "client", "connection",
		)
		require.Error(t, err)
		assert.ErrorIs(t, err, errNoSystemAccessTokenEnvVar)
	})

	t.Run("Success", func(t *testing.T) {
		t.Setenv("SYSTEM_ACCESSTOKEN", "fake-token")
		t.Setenv(
			"SYSTEM_OIDCREQUESTURI",
			"https://vstoken.dev.azure.com/test",
		)

		credCache := &memoryCache{cache: make(map[string][]byte)}
		m := Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: newMemoryUserConfigManager(),
			credentialCache:   credCache,
			cloud:             cloud.AzurePublic(),
			publicClient:      &mockPublicClient{},
		}

		cred, err := m.LoginWithAzurePipelinesFederatedTokenProvider(
			t.Context(), "testTenant", "testClient", "testConnection",
		)
		require.NoError(t, err)
		require.IsType(t, new(azidentity.AzurePipelinesCredential), cred)

		// Verify it saved and can roundtrip
		cred2, err := m.CredentialForCurrentUser(t.Context(), nil)
		require.NoError(t, err)
		require.IsType(t, new(azidentity.AzurePipelinesCredential), cred2)
	})
}

// --- LoginWithOidcFederatedTokenProvider ---

func TestLoginWithOidcFederatedTokenProvider(t *testing.T) {
	t.Run("WithDirectIDToken", func(t *testing.T) {
		t.Setenv("AZURE_OIDC_TOKEN", "fake-id-token")

		credCache := &memoryCache{cache: make(map[string][]byte)}
		m := Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: newMemoryUserConfigManager(),
			credentialCache:   credCache,
			cloud:             cloud.AzurePublic(),
			publicClient:      &mockPublicClient{},
		}

		cred, err := m.LoginWithOidcFederatedTokenProvider(
			t.Context(), "testTenant", "testClient",
		)
		require.NoError(t, err)
		require.IsType(t, new(azidentity.ClientAssertionCredential), cred)
	})

	t.Run("MissingAllEnvVars", func(t *testing.T) {
		// Ensure none of the env vars are set
		t.Setenv("AZURE_OIDC_TOKEN", "")
		os.Unsetenv("AZURE_OIDC_TOKEN")
		os.Unsetenv("AZURE_OIDC_REQUEST_TOKEN")
		os.Unsetenv("AZURE_OIDC_REQUEST_URL")

		credCache := &memoryCache{cache: make(map[string][]byte)}
		m := Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: newMemoryUserConfigManager(),
			credentialCache:   credCache,
			cloud:             cloud.AzurePublic(),
		}

		_, err := m.LoginWithOidcFederatedTokenProvider(
			t.Context(), "testTenant", "testClient",
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required values")
	})
}

// --- Logout branches ---

func TestLogout_ServicePrincipal(t *testing.T) {
	credCache := &memoryCache{cache: make(map[string][]byte)}
	m := Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		credentialCache:   credCache,
		cloud:             cloud.AzurePublic(),
		publicClient:      &mockPublicClient{},
	}

	// Login as SP
	_, err := m.LoginWithServicePrincipalSecret(
		t.Context(), "testTenant", "testClient", "testSecret",
	)
	require.NoError(t, err)

	// Verify logged in
	_, err = m.CredentialForCurrentUser(t.Context(), nil)
	require.NoError(t, err)

	// Logout
	err = m.Logout(t.Context())
	require.NoError(t, err)

	// Verify logged out
	_, err = m.CredentialForCurrentUser(t.Context(), nil)
	require.ErrorIs(t, err, ErrNoCurrentUser)
}

func TestLogout_UserAccount(t *testing.T) {
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient:      &mockPublicClient{},
		cloud:             cloud.AzurePublic(),
	}

	// Login interactively
	_, err := m.LoginInteractive(t.Context(), nil, "", nil)
	require.NoError(t, err)

	// Logout
	err = m.Logout(t.Context())
	require.NoError(t, err)

	// Verify logged out
	_, err = m.CredentialForCurrentUser(t.Context(), nil)
	require.ErrorIs(t, err, ErrNoCurrentUser)
}

func TestLogout_NotLoggedIn(t *testing.T) {
	m := Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient:      &mockPublicClient{},
	}

	// Should succeed even when not logged in
	err := m.Logout(t.Context())
	require.NoError(t, err)
}

// --- EnsureLoggedInCredential ---

func TestEnsureLoggedInCredential_Success(t *testing.T) {
	cred := &fakeTokenCredential{
		token: azcore.AccessToken{Token: "fake-token"},
	}
	tok, err := EnsureLoggedInCredential(t.Context(), cred, cloud.AzurePublic())
	require.NoError(t, err)
	assert.Equal(t, "fake-token", tok.Token)
}

func TestEnsureLoggedInCredential_Error(t *testing.T) {
	cred := &fakeTokenCredential{
		err: errors.New("auth failed"),
	}
	_, err := EnsureLoggedInCredential(t.Context(), cred, cloud.AzurePublic())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth failed")
}

// --- Cloud ---

func TestManager_Cloud(t *testing.T) {
	c := cloud.AzurePublic()
	m := Manager{cloud: c}
	assert.Equal(t, c, m.Cloud())
}

// --- authClientOptions ---

func TestAuthClientOptions_WithUserAgent(t *testing.T) {
	m := Manager{
		cloud:     cloud.AzurePublic(),
		userAgent: "test-agent/1.0",
	}
	opts := m.authClientOptions()
	assert.Equal(t, "test-agent/1.0", opts.Telemetry.ApplicationID)
}

func TestAuthClientOptions_WithoutUserAgent(t *testing.T) {
	m := Manager{
		cloud: cloud.AzurePublic(),
	}
	opts := m.authClientOptions()
	assert.Empty(t, opts.Telemetry.ApplicationID)
}

// --- LogInDetails non-legacy paths ---

func TestLogInDetails_ServicePrincipalNative(t *testing.T) {
	credCache := &memoryCache{cache: make(map[string][]byte)}
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		credentialCache:   credCache,
		cloud:             cloud.AzurePublic(),
		publicClient:      &mockPublicClient{},
	}

	_, err := m.LoginWithServicePrincipalSecret(
		t.Context(), "testTenant", "myClientId", "testSecret",
	)
	require.NoError(t, err)

	details, err := m.LogInDetails(t.Context())
	require.NoError(t, err)
	assert.Equal(t, ClientIdLoginType, details.LoginType)
	assert.Equal(t, "myClientId", details.Account)
}

func TestLogInDetails_InteractiveUser(t *testing.T) {
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient: &mockPublicClientWithAccounts{
			accounts: []public.Account{
				{
					HomeAccountID:     "test.id",
					PreferredUsername: "user@example.com",
				},
			},
		},
		cloud: cloud.AzurePublic(),
	}

	// Save a user with HomeAccountID matching the mock accounts
	err := m.saveUserProperties(&userProperties{
		HomeAccountID: new("test.id"),
	})
	require.NoError(t, err)

	details, err := m.LogInDetails(t.Context())
	require.NoError(t, err)
	assert.Equal(t, EmailLoginType, details.LoginType)
	assert.Equal(t, "user@example.com", details.Account)
}

func TestLogInDetails_NotLoggedIn(t *testing.T) {
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient:      &mockPublicClient{},
	}

	_, err := m.LogInDetails(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoCurrentUser)
}

func TestLogInDetails_HomeAccountNotFound(t *testing.T) {
	emptyAccounts := &mockPublicClientWithAccounts{
		accounts: []public.Account{},
	}

	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient:      emptyAccounts,
	}

	// Save user with HomeAccountID that won't match any account
	err := m.saveUserProperties(&userProperties{
		HomeAccountID: new("missing-id"),
	})
	require.NoError(t, err)

	_, err = m.LogInDetails(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoCurrentUser)
}

// --- UseExternalAuth ---

func TestUseExternalAuth(t *testing.T) {
	t.Run("BothSet", func(t *testing.T) {
		m := Manager{
			externalAuthCfg: ExternalAuthConfiguration{
				Endpoint: "https://example.com",
				Key:      "key",
			},
		}
		assert.True(t, m.UseExternalAuth())
	})

	t.Run("OnlyEndpoint", func(t *testing.T) {
		m := Manager{
			externalAuthCfg: ExternalAuthConfiguration{
				Endpoint: "https://example.com",
			},
		}
		assert.False(t, m.UseExternalAuth())
	})

	t.Run("OnlyKey", func(t *testing.T) {
		m := Manager{
			externalAuthCfg: ExternalAuthConfiguration{
				Key: "key",
			},
		}
		assert.False(t, m.UseExternalAuth())
	})

	t.Run("NeitherSet", func(t *testing.T) {
		m := Manager{}
		assert.False(t, m.UseExternalAuth())
	})
}

// --- LoginScopes --- (covered in claims_test.go)

// --- shouldUseLegacyAuth ---

func TestShouldUseLegacyAuth(t *testing.T) {
	tests := []struct {
		name     string
		val      string
		expected bool
	}{
		{"TrueString", "true", true},
		{"OneString", "1", true},
		{"FalseString", "false", false},
		{"ZeroString", "0", false},
		{"InvalidString", "notabool", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newMemoryUserConfigManager()
			c, _ := cfg.Load()
			require.NoError(t, c.Set(useAzCliAuthKey, tt.val))
			assert.Equal(t, tt.expected, shouldUseLegacyAuth(c))
		})
	}

	t.Run("NotSet", func(t *testing.T) {
		cfg := newMemoryUserConfigManager()
		c, _ := cfg.Load()
		assert.False(t, shouldUseLegacyAuth(c))
	})
}

// --- readUserProperties edge cases ---

func TestReadUserProperties_NoCurrentUser(t *testing.T) {
	cfg := newMemoryUserConfigManager()
	c, _ := cfg.Load()
	_, err := readUserProperties(c)
	require.ErrorIs(t, err, ErrNoCurrentUser)
}

// --- persistedSecretLookupKey ---

func TestPersistedSecretLookupKey(t *testing.T) {
	key := persistedSecretLookupKey("tenant123", "client456")
	assert.Equal(t, "tenant123.client456", key)
}

// --- newCredentialFromFederatedTokenProvider edge cases ---

func TestNewCredentialFromFederatedTokenProvider_AzurePipelines_NoServiceConnectionID(t *testing.T) {
	t.Setenv("SYSTEM_ACCESSTOKEN", "fake-token")

	m := Manager{cloud: cloud.AzurePublic()}
	_, err := m.newCredentialFromFederatedTokenProvider(
		"tenant", "client", azurePipelinesFederatedTokenProvider, nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service connection ID not found")
}

func TestNewCredentialFromFederatedTokenProvider_AzurePipelines_MissingToken(t *testing.T) {
	// Don't set SYSTEM_ACCESSTOKEN
	m := Manager{cloud: cloud.AzurePublic()}
	_, err := m.newCredentialFromFederatedTokenProvider(
		"tenant", "client", azurePipelinesFederatedTokenProvider, nil,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, errNoSystemAccessTokenEnvVar)
}

func TestNewCredentialFromFederatedTokenProvider_GitHub_MissingEnvVars(t *testing.T) {
	// Ensure env vars are not set
	os.Unsetenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	os.Unsetenv("ACTIONS_ID_TOKEN_REQUEST_URL")

	m := Manager{cloud: cloud.AzurePublic()}
	_, err := m.newCredentialFromFederatedTokenProvider(
		"tenant", "client", gitHubFederatedTokenProvider, nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ACTIONS_ID_TOKEN_REQUEST_TOKEN")
}

func TestNewCredentialFromFederatedTokenProvider_GitHub_MissingURL(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "token-value")
	os.Unsetenv("ACTIONS_ID_TOKEN_REQUEST_URL")

	m := Manager{cloud: cloud.AzurePublic()}
	_, err := m.newCredentialFromFederatedTokenProvider(
		"tenant", "client", gitHubFederatedTokenProvider, nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ACTIONS_ID_TOKEN_REQUEST_URL")
}

func TestNewCredentialFromFederatedTokenProvider_Oidc_MissingRequestToken(t *testing.T) {
	os.Unsetenv("AZURE_OIDC_TOKEN")
	t.Setenv("AZURE_OIDC_REQUEST_URL", "https://example.com")
	os.Unsetenv("AZURE_OIDC_REQUEST_TOKEN")

	m := Manager{cloud: cloud.AzurePublic()}
	_, err := m.newCredentialFromFederatedTokenProvider(
		"tenant", "client", oidcFederatedTokenProvider, nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AZURE_OIDC_REQUEST_TOKEN")
}

func TestNewCredentialFromFederatedTokenProvider_Oidc_MissingRequestURL(t *testing.T) {
	os.Unsetenv("AZURE_OIDC_TOKEN")
	t.Setenv("AZURE_OIDC_REQUEST_TOKEN", "token-value")
	os.Unsetenv("AZURE_OIDC_REQUEST_URL")

	m := Manager{cloud: cloud.AzurePublic()}
	_, err := m.newCredentialFromFederatedTokenProvider(
		"tenant", "client", oidcFederatedTokenProvider, nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AZURE_OIDC_REQUEST_URL")
}

func TestNewCredentialFromFederatedTokenProvider_Oidc_WithDirectToken(t *testing.T) {
	t.Setenv("AZURE_OIDC_TOKEN", "my-oidc-token")

	m := Manager{cloud: cloud.AzurePublic()}
	cred, err := m.newCredentialFromFederatedTokenProvider(
		"tenant", "client", oidcFederatedTokenProvider, nil,
	)
	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientAssertionCredential), cred)
}

func TestNewCredentialFromFederatedTokenProvider_Oidc_WithRequestTokenAndURL(t *testing.T) {
	os.Unsetenv("AZURE_OIDC_TOKEN")
	t.Setenv("AZURE_OIDC_REQUEST_TOKEN", "request-token")
	t.Setenv("AZURE_OIDC_REQUEST_URL", "https://example.com/token")

	m := Manager{cloud: cloud.AzurePublic()}
	cred, err := m.newCredentialFromFederatedTokenProvider(
		"tenant", "client", oidcFederatedTokenProvider, nil,
	)
	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientAssertionCredential), cred)
}

func TestNewCredentialFromFederatedTokenProvider_UnsupportedProvider(t *testing.T) {
	m := Manager{cloud: cloud.AzurePublic()}
	_, err := m.newCredentialFromFederatedTokenProvider(
		"tenant", "client", federatedTokenProvider("unknown-provider"), nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported federated token provider")
}

// --- Certificate credential roundtrip ---

func TestCredentialForCurrentUser_CertificateRoundtrip(t *testing.T) {
	credCache := &memoryCache{cache: make(map[string][]byte)}
	m := Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		credentialCache:   credCache,
		cloud:             cloud.AzurePublic(),
		publicClient:      &mockPublicClient{},
	}

	_, err := m.LoginWithServicePrincipalCertificate(
		t.Context(), "testTenant", "testClient", testClientCertificate,
	)
	require.NoError(t, err)

	// Request with tenant override
	cred, err := m.CredentialForCurrentUser(t.Context(), &CredentialForCurrentUserOptions{
		TenantID: "overrideTenant",
	})
	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientCertificateCredential), cred)
}

// --- Helper types ---

// fakeTokenCredential implements azcore.TokenCredential for testing.
type fakeTokenCredential struct {
	token azcore.AccessToken
	err   error
}

func (f *fakeTokenCredential) GetToken(
	_ context.Context, _ policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return f.token, f.err
}

// mockPublicClientWithAccounts extends mockPublicClient with configurable accounts.
type mockPublicClientWithAccounts struct {
	accounts []public.Account
}

func (m *mockPublicClientWithAccounts) Accounts(_ context.Context) ([]public.Account, error) {
	return m.accounts, nil
}

func (m *mockPublicClientWithAccounts) RemoveAccount(_ context.Context, _ public.Account) error {
	return nil
}

func (m *mockPublicClientWithAccounts) AcquireTokenInteractive(
	_ context.Context, _ []string, _ ...public.AcquireInteractiveOption,
) (public.AuthResult, error) {
	if len(m.accounts) > 0 {
		return public.AuthResult{Account: m.accounts[0]}, nil
	}
	return public.AuthResult{}, nil
}

func (m *mockPublicClientWithAccounts) AcquireTokenSilent(
	_ context.Context, _ []string, _ ...public.AcquireSilentOption,
) (public.AuthResult, error) {
	if len(m.accounts) > 0 {
		return public.AuthResult{Account: m.accounts[0]}, nil
	}
	return public.AuthResult{}, nil
}

func (m *mockPublicClientWithAccounts) AcquireTokenByDeviceCode(
	_ context.Context, _ []string, _ ...public.AcquireByDeviceCodeOption,
) (deviceCodeResult, error) {
	return &mockDeviceCode{}, nil
}
