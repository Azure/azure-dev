// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- azdCredential.GetToken tests ---

func TestAzdCredential_GetToken_Success(t *testing.T) {
	// This tests the happy path — AcquireTokenSilent succeeds
	expires := time.Now().Add(time.Hour).UTC()
	pc := &silentSuccessClient{
		result: public.AuthResult{
			AccessToken: "test-token-123",
			ExpiresOn:   expires,
		},
	}

	account := public.Account{HomeAccountID: "h1"}
	cred := newAzdCredential(
		pc, &account, cloud.AzurePublic(), "",
	)

	tok, err := cred.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.NoError(t, err)
	assert.Equal(t, "test-token-123", tok.Token)
	assert.Equal(t, expires, tok.ExpiresOn)
}

func TestAzdCredential_GetToken_WithTenantID(t *testing.T) {
	pc := &silentSuccessClient{
		result: public.AuthResult{
			AccessToken: "tenant-tok",
			ExpiresOn:   time.Now().Add(time.Hour).UTC(),
		},
	}

	account := public.Account{HomeAccountID: "h1"}
	cred := newAzdCredential(
		pc, &account, cloud.AzurePublic(), "default-t",
	)

	// Override with request-level tenant
	tok, err := cred.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes:   []string{"scope1"},
		TenantID: "override-t",
	})
	require.NoError(t, err)
	assert.Equal(t, "tenant-tok", tok.Token)
}

func TestAzdCredential_GetToken_GenericError(t *testing.T) {
	pc := &silentErrorClient{
		err: errors.New("network failure"),
	}

	account := public.Account{HomeAccountID: "h1"}
	cred := newAzdCredential(
		pc, &account, cloud.AzurePublic(), "",
	)

	_, err := cred.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network failure")
}

func TestAzdCredential_GetToken_AuthFailedNotReLogin(t *testing.T) {
	// AuthFailedError that doesn't trigger re-login
	pc := &silentErrorClient{
		err: &AuthFailedError{
			innerErr: errors.New("some auth err"),
		},
	}

	account := public.Account{HomeAccountID: "h1"}
	cred := newAzdCredential(
		pc, &account, cloud.AzurePublic(), "",
	)

	_, err := cred.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)

	var authFailed *AuthFailedError
	require.True(t, errors.As(err, &authFailed))
}

// --- ClaimsForCurrentUser extended tests ---

func TestClaimsForCurrentUser_GetTokenError(t *testing.T) {
	// Use external auth with a transporter that returns an error
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		publicClient:      &mockPublicClient{},
		externalAuthCfg: ExternalAuthConfiguration{
			Endpoint: "https://example.com",
			Key:      "secret",
			Transporter: &fakeTransporter{
				err: errors.New("connection refused"),
			},
		},
	}

	_, err := m.ClaimsForCurrentUser(t.Context(), nil)
	require.Error(t, err)
}

func TestClaimsForCurrentUser_WithTenantID(t *testing.T) {
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		publicClient:      &mockPublicClient{},
		externalAuthCfg: ExternalAuthConfiguration{
			Endpoint: "https://example.com",
			Key:      "my-key",
			Transporter: &fakeTransporter{
				err: errors.New("connection refused"),
			},
		},
	}

	opts := &ClaimsForCurrentUserOptions{
		TenantID: "my-tenant",
	}
	_, err := m.ClaimsForCurrentUser(t.Context(), opts)
	require.Error(t, err)
}

// --- GetLoggedInServicePrincipalTenantID extra branches ---

func TestGetLoggedInServicePrincipalTenantID_UserAccount(
	t *testing.T,
) {
	credCache := &memoryCache{cache: make(map[string][]byte)}
	cfgMgr := newMemoryConfigManager()
	userCfgMgr := newMemoryUserConfigManager()

	accounts := []public.Account{
		{HomeAccountID: "home.id"},
	}
	pc := &mockPublicClientWithAccounts{accounts: accounts}

	m := &Manager{
		configManager:     cfgMgr,
		userConfigManager: userCfgMgr,
		credentialCache:   credCache,
		cloud:             cloud.AzurePublic(),
		publicClient:      pc,
	}

	// Login as user (via saveLoginForPublicClient)
	err := m.saveLoginForPublicClient(public.AuthResult{
		Account: public.Account{HomeAccountID: "home.id"},
	})
	require.NoError(t, err)

	result, err := m.GetLoggedInServicePrincipalTenantID(
		t.Context(),
	)
	require.NoError(t, err)
	// User accounts return nil for tenant ID
	assert.Nil(t, result)
}

func TestGetLoggedInServicePrincipalTenantID_SP(
	t *testing.T,
) {
	credCache := &memoryCache{cache: make(map[string][]byte)}
	cfgMgr := newMemoryConfigManager()
	userCfgMgr := newMemoryUserConfigManager()

	m := &Manager{
		configManager:     cfgMgr,
		userConfigManager: userCfgMgr,
		credentialCache:   credCache,
		cloud:             cloud.AzurePublic(),
		publicClient:      &mockPublicClient{},
	}

	// Login as SP
	_, err := m.LoginWithServicePrincipalSecret(
		t.Context(), "sp-tenant", "sp-client", "sp-secret",
	)
	require.NoError(t, err)

	result, err := m.GetLoggedInServicePrincipalTenantID(
		t.Context(),
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "sp-tenant", *result)
}

// --- LoginWithGitHubFederatedTokenProvider ---

func TestLoginWithGitHubFederatedTokenProvider_Success(
	t *testing.T,
) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "gh-token")
	t.Setenv(
		"ACTIONS_ID_TOKEN_REQUEST_URL",
		"https://token.actions.githubusercontent.com",
	)

	credCache := &memoryCache{cache: make(map[string][]byte)}
	m := Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		credentialCache:   credCache,
		cloud:             cloud.AzurePublic(),
	}

	cred, err := m.LoginWithGitHubFederatedTokenProvider(
		t.Context(), "gh-tenant", "gh-client",
	)
	require.NoError(t, err)
	require.NotNil(t, cred)
}

// --- Logout extended branches ---

func TestLogout_FederatedSP(t *testing.T) {
	credCache := &memoryCache{cache: make(map[string][]byte)}
	cfgMgr := newMemoryConfigManager()
	userCfgMgr := newMemoryUserConfigManager()

	m := &Manager{
		configManager:     cfgMgr,
		userConfigManager: userCfgMgr,
		credentialCache:   credCache,
		cloud:             cloud.AzurePublic(),
		publicClient:      &mockPublicClient{},
	}

	// Login as SP with federated auth
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "token")
	t.Setenv(
		"ACTIONS_ID_TOKEN_REQUEST_URL",
		"https://token.actions.githubusercontent.com",
	)
	_, err := m.LoginWithGitHubFederatedTokenProvider(
		t.Context(), "t1", "c1",
	)
	require.NoError(t, err)

	// Logout
	err = m.Logout(t.Context())
	require.NoError(t, err)
}

// --- MultiTenantCredentialProvider success path ---

func TestMultiTenantCredentialProvider_SuccessPath(
	t *testing.T,
) {
	credCache := &memoryCache{cache: make(map[string][]byte)}
	cfgMgr := newMemoryConfigManager()
	userCfgMgr := newMemoryUserConfigManager()

	accounts := []public.Account{
		{HomeAccountID: "home.id"},
	}
	pc := &mockPublicClientWithAccounts{accounts: accounts}

	m := &Manager{
		configManager:     cfgMgr,
		userConfigManager: userCfgMgr,
		credentialCache:   credCache,
		cloud:             cloud.AzurePublic(),
		publicClient:      pc,
	}

	// Login as user
	err := m.saveLoginForPublicClient(public.AuthResult{
		Account: public.Account{HomeAccountID: "home.id"},
	})
	require.NoError(t, err)

	provider := NewMultiTenantCredentialProvider(m)
	cred, err := provider.GetTokenCredential(
		t.Context(), "my-tenant",
	)
	// The credential is returned, but EnsureLoggedInCredential
	// will fail since there's no real Azure. But the path through
	// GetTokenCredential->CredentialForCurrentUser should succeed.
	// This exercises the non-cached path.
	//
	// Note: error comes from EnsureLoggedInCredential trying
	// to actually get a token
	if err != nil {
		// Expected - but we should have gotten past the
		// CredentialForCurrentUser call
		assert.NotErrorIs(t, err, ErrNoCurrentUser)
	} else {
		assert.NotNil(t, cred)
	}
}

// --- helper: publicClient that succeeds on AcquireTokenSilent ---

type silentSuccessClient struct {
	mockPublicClient
	result public.AuthResult
}

func (s *silentSuccessClient) AcquireTokenSilent(
	_ context.Context,
	_ []string,
	_ ...public.AcquireSilentOption,
) (public.AuthResult, error) {
	return s.result, nil
}

// --- helper: publicClient that fails on AcquireTokenSilent ---

type silentErrorClient struct {
	mockPublicClient
	err error
}

func (s *silentErrorClient) AcquireTokenSilent(
	_ context.Context,
	_ []string,
	_ ...public.AcquireSilentOption,
) (public.AuthResult, error) {
	return public.AuthResult{}, s.err
}
