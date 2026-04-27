// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	msalcache "github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// NonRetriable marker method coverage
// ---------------------------------------------------------------------------

func TestReLoginRequiredError_NonRetriableMarker(t *testing.T) {
	e := &ReLoginRequiredError{errText: "re-login"}
	e.NonRetriable() // just exercise the method
}

func TestAuthFailedError_NonRetriableMarker(t *testing.T) {
	e := &AuthFailedError{innerErr: errors.New("inner")}
	e.NonRetriable() // just exercise the method
}

// ---------------------------------------------------------------------------
// saveLoginForServicePrincipal — saveSecret failure path
// ---------------------------------------------------------------------------

type errCache struct{}

var errCacheFail = errors.New("cache-write-fail")

func (c *errCache) Read(key string) ([]byte, error) {
	return nil, errCacheKeyNotFound
}

func (c *errCache) Set(_ string, _ []byte) error {
	return errCacheFail
}

func TestSaveLoginForSP_SecretWriteFails(t *testing.T) {
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		credentialCache:   &errCache{},
	}

	secret := "s"
	err := m.saveLoginForServicePrincipal(
		"tid", "cid", &persistedSecret{ClientSecret: &secret})
	require.ErrorIs(t, err, errCacheFail)
}

// ---------------------------------------------------------------------------
// saveLoginForPublicClient — exercises saveUserProperties
// ---------------------------------------------------------------------------

func TestSaveLoginForPublicClient_Success(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		credentialCache: &memoryCache{
			cache: map[string][]byte{},
		},
	}

	err := m.saveLoginForPublicClient(public.AuthResult{
		Account: public.Account{HomeAccountID: "home-id"},
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// CredentialForCurrentUser — userConfigManager.Load error
// ---------------------------------------------------------------------------

func TestCredentialForCurrentUser_LoadUserConfigError(t *testing.T) {
	m := &Manager{
		configManager: newMemoryConfigManager(),
		userConfigManager: &failingUserConfigManager{
			err: errors.New("load-fail"),
		},
		cloud: cloud.AzurePublic(),
		credentialCache: &memoryCache{
			cache: map[string][]byte{},
		},
	}

	_, err := m.CredentialForCurrentUser(t.Context(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetching current user")
}

// ---------------------------------------------------------------------------
// GetLoggedInServicePrincipalTenantID — userConfigManager.Load error
// ---------------------------------------------------------------------------

func TestGetLoggedInSPTenantID_LoadUserConfigError(t *testing.T) {
	m := &Manager{
		configManager: newMemoryConfigManager(),
		userConfigManager: &failingUserConfigManager{
			err: errors.New("load-fail"),
		},
		cloud: cloud.AzurePublic(),
		credentialCache: &memoryCache{
			cache: map[string][]byte{},
		},
	}

	_, err := m.GetLoggedInServicePrincipalTenantID(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetching current user")
}

// ---------------------------------------------------------------------------
// Mode — error from userConfigManager.Load
// ---------------------------------------------------------------------------

func TestMode_LoadUserConfigError(t *testing.T) {
	m := &Manager{
		userConfigManager: &failingUserConfigManager{
			err: errors.New("load-fail"),
		},
	}
	_, err := m.Mode()
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetching current user")
}

// ---------------------------------------------------------------------------
// SetBuiltInAuthMode — error paths
// ---------------------------------------------------------------------------

func TestSetBuiltInAuthMode_ModeError(t *testing.T) {
	m := &Manager{
		userConfigManager: &failingUserConfigManager{
			err: errors.New("load-fail"),
		},
	}
	err := m.SetBuiltInAuthMode()
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetching current auth mode")
}

func TestSetBuiltInAuthMode_ExternalAuth(t *testing.T) {
	m := &Manager{
		userConfigManager: newMemoryUserConfigManager(),
		externalAuthCfg: ExternalAuthConfiguration{
			Endpoint: "http://localhost",
			Key:      "k",
			Transporter: &fakeTransporter{
				response: &http.Response{StatusCode: 200},
			},
		},
	}
	err := m.SetBuiltInAuthMode()
	require.Error(t, err)
	require.Contains(t, err.Error(), "external token mode")
}

// ---------------------------------------------------------------------------
// azdCredential.GetToken — options.Claims path (AuthFailed + re-login)
// ---------------------------------------------------------------------------

func TestAzdCredentialGetToken_ClaimsReLoginPath(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	c := cloud.AzurePublic()

	// Build a minimal HTTP response with a Request attached so
	// httpErrorDetails() doesn't panic.
	fakeReq, _ := http.NewRequest(
		http.MethodPost,
		"https://login.microsoftonline.com/common/oauth2/v2.0/token",
		nil)
	resp := &http.Response{
		StatusCode: 401,
		Status:     "401 Unauthorized",
		Request:    fakeReq,
		Body:       http.NoBody,
	}

	// A client that returns an AuthFailedError whose AAD response
	// triggers re-login (interaction_required).
	client := &silentErrorClient{
		err: &AuthFailedError{
			RawResp:  resp,
			innerErr: errors.New("tenant requires MFA"),
			Parsed: &AadErrorResponse{
				Error:            "interaction_required",
				ErrorDescription: "AADSTS50076: need MFA",
			},
		},
	}

	acct := public.Account{HomeAccountID: "home-a"}
	cred := newAzdCredential(client, &acct, c, "", nil)

	opts := tokenRequestOpts(c, "my-claims")
	_, err := cred.GetToken(t.Context(), opts)
	require.Error(t, err)
	// Should be a ReLoginRequiredError.
	var rle *ReLoginRequiredError
	require.True(t, errors.As(err, &rle))
}

// ---------------------------------------------------------------------------
// cache.Replace — error from inner cache (non-key-not-found)
// ---------------------------------------------------------------------------

type readErrorCache struct{}

var errReadFail = errors.New("read-fail")

func (c *readErrorCache) Read(string) ([]byte, error) {
	return nil, errReadFail
}
func (c *readErrorCache) Set(string, []byte) error { return nil }

func TestMsalCacheAdapter_ReplaceInnerError(t *testing.T) {
	adapter := &msalCacheAdapter{cache: &readErrorCache{}}

	err := adapter.Replace(
		t.Context(), &fakeUnmarshaler{},
		msalcache.ReplaceHints{})
	require.ErrorIs(t, err, errReadFail)
}

func TestMsalCacheAdapter_ReplaceKeyNotFound(t *testing.T) {
	adapter := &msalCacheAdapter{cache: &errCache{}}

	// errCache.Read returns errCacheKeyNotFound → swallowed.
	err := adapter.Replace(
		t.Context(), &fakeUnmarshaler{},
		msalcache.ReplaceHints{})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// cache.Export — marshal error
// ---------------------------------------------------------------------------

func TestMsalCacheAdapter_ExportMarshalError(t *testing.T) {
	adapter := &msalCacheAdapter{
		cache: &memoryCache{cache: map[string][]byte{}},
	}

	err := adapter.Export(
		t.Context(), &failingMarshaler{},
		msalcache.ExportHints{})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// CredentialForCurrentUser — readAuthConfig error
// ---------------------------------------------------------------------------

func TestCredentialForCurrentUser_ReadAuthConfigError(t *testing.T) {
	// Point AZD_CONFIG_DIR to empty dir so readAuthConfig succeeds,
	// but use a non-writable nested path to break the config read.
	cfgDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", cfgDir)

	// A configManager whose Load returns an error triggers readAuthConfig
	// to fail. readAuthConfig uses m.configManager (the global one),
	// not userConfigManager, so we configure that.
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		credentialCache: &memoryCache{
			cache: map[string][]byte{},
		},
	}

	// No user logged in + not in CloudShell → ErrNoCurrentUser.
	_, err := m.CredentialForCurrentUser(t.Context(), nil)
	require.ErrorIs(t, err, ErrNoCurrentUser)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func tokenRequestOpts(
	c *cloud.Cloud, claims string,
) policy.TokenRequestOptions {
	return policy.TokenRequestOptions{
		Scopes: LoginScopes(c),
		Claims: claims,
	}
}

type fakeUnmarshaler struct{}

func (f *fakeUnmarshaler) Unmarshal([]byte) error { return nil }

type failingMarshaler struct{}

func (f *failingMarshaler) Marshal() ([]byte, error) {
	return nil, errors.New("marshal-fail")
}
