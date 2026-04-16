// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/az"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

var errWave3Test = errors.New("wave3-test")

// ---------- helpers ----------

// fakeJWT builds a minimal JWT whose payload encodes the given claims.
func fakeJWT(t *testing.T, claims TokenClaims) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)

	header := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"alg":"none","typ":"JWT"}`))
	body := base64.RawURLEncoding.EncodeToString(payload)
	sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	return header + "." + body + "." + sig
}

// mockPublicClientFull supports all publicClient methods with
// configurable results for Accounts / AcquireTokenInteractive /
// AcquireTokenByDeviceCode / AcquireTokenSilent / RemoveAccount.
type mockPublicClientFull struct {
	accounts         []public.Account
	accountsErr      error
	interactiveRes   public.AuthResult
	interactiveErr   error
	deviceCodeResult deviceCodeResult
	deviceCodeErr    error
	silentRes        public.AuthResult
	silentErr        error
	removeErr        error
}

func (m *mockPublicClientFull) Accounts(
	_ context.Context,
) ([]public.Account, error) {
	return m.accounts, m.accountsErr
}

func (m *mockPublicClientFull) RemoveAccount(
	_ context.Context, _ public.Account,
) error {
	return m.removeErr
}

func (m *mockPublicClientFull) AcquireTokenInteractive(
	_ context.Context, _ []string,
	_ ...public.AcquireInteractiveOption,
) (public.AuthResult, error) {
	return m.interactiveRes, m.interactiveErr
}

func (m *mockPublicClientFull) AcquireTokenByDeviceCode(
	_ context.Context, _ []string,
	_ ...public.AcquireByDeviceCodeOption,
) (deviceCodeResult, error) {
	return m.deviceCodeResult, m.deviceCodeErr
}

func (m *mockPublicClientFull) AcquireTokenSilent(
	_ context.Context, _ []string,
	_ ...public.AcquireSilentOption,
) (public.AuthResult, error) {
	return m.silentRes, m.silentErr
}

// stubDeviceCode is a minimal deviceCodeResult.
type stubDeviceCode struct {
	msg  string
	code string
	res  public.AuthResult
	err  error
}

func (s *stubDeviceCode) Message() string  { return s.msg }
func (s *stubDeviceCode) UserCode() string { return s.code }
func (s *stubDeviceCode) AuthenticationResult(
	_ context.Context,
) (public.AuthResult, error) {
	return s.res, s.err
}

// ---------- NewManager ----------

func TestNewManager_Success(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	mgr, err := NewManager(
		newMemoryConfigManager(),
		newMemoryUserConfigManager(),
		cloud.AzurePublic(),
		http.DefaultClient,
		nil, // console not needed
		ExternalAuthConfiguration{},
		az.AzCli{},
		"test-agent",
	)
	require.NoError(t, err)
	require.NotNil(t, mgr)
}

func TestNewManager_EmptyUserAgent(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	mgr, err := NewManager(
		newMemoryConfigManager(),
		newMemoryUserConfigManager(),
		cloud.AzurePublic(),
		http.DefaultClient,
		nil,
		ExternalAuthConfiguration{},
		az.AzCli{},
		"", // empty user-agent — exercises the bypass in newUserAgentClient
	)
	require.NoError(t, err)
	require.NotNil(t, mgr)
}

// ---------- saveClaims / loadClaims / claimsFilePath ----------

func TestSaveClaims_RoundTrip(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	validJSON := `{"access_token":{"essential":true}}`
	require.NoError(t, saveClaims(validJSON))

	claims, path, err := loadClaims()
	require.NoError(t, err)
	require.Equal(t, validJSON, claims)
	require.NotEmpty(t, path)
}

func TestLoadClaims_NoFile(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	claims, path, err := loadClaims()
	require.NoError(t, err)
	require.Empty(t, claims)
	require.Empty(t, path)
}

func TestLoadClaims_InvalidJSON_RemovesFile(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	// Write invalid JSON manually.
	fp, err := claimsFilePath()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fp, []byte("not-json"), 0600))

	claims, path, err := loadClaims()
	require.NoError(t, err)
	require.Empty(t, claims)
	require.Empty(t, path)

	// File should have been removed.
	_, statErr := os.Stat(fp)
	require.True(t, os.IsNotExist(statErr))
}

// ---------- ClaimsForCurrentUser — success path ----------

func TestClaimsForCurrentUser_ParsesJWT(t *testing.T) {
	cfgMgr := newMemoryConfigManager()
	userCfgMgr := newMemoryUserConfigManager()
	c := cloud.AzurePublic()

	// Prepare a logged-in user by writing auth config with
	// a HomeAccountID that matches the mock client's account.
	homeID := "jwt-test-user"
	m := &Manager{
		configManager:     cfgMgr,
		userConfigManager: userCfgMgr,
		cloud:             c,
		publicClient: &mockPublicClientFull{
			accounts: []public.Account{
				{HomeAccountID: homeID},
			},
			silentRes: public.AuthResult{
				AccessToken: fakeJWT(t, TokenClaims{
					PreferredUsername: "alice@example.com",
					Oid:               "oid-1",
					TenantId:          "tid-1",
				}),
				Account: public.Account{
					HomeAccountID: homeID,
				},
			},
		},
	}

	// Log in so that readAuthConfig → readUserProperties succeed.
	require.NoError(t,
		m.saveUserProperties(&userProperties{
			HomeAccountID: &homeID,
		}))

	claims, err := m.ClaimsForCurrentUser(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", claims.PreferredUsername)
	require.Equal(t, "oid-1", claims.Oid)
	require.Equal(t, "tid-1", claims.TenantId)
}

// ---------- LoginInteractive ----------

func TestLoginInteractive_Success(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	homeID := "interactive-home"
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		publicClient: &mockPublicClientFull{
			interactiveRes: public.AuthResult{
				Account: public.Account{
					HomeAccountID: homeID,
				},
			},
		},
	}

	cred, err := m.LoginInteractive(
		t.Context(), nil, "", nil)
	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)
}

func TestLoginInteractive_WithAllOptions(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	homeID := "interactive-opts"
	openURLCalled := false

	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		publicClient: &mockPublicClientFull{
			interactiveRes: public.AuthResult{
				Account: public.Account{
					HomeAccountID: homeID,
				},
			},
		},
	}

	cred, err := m.LoginInteractive(
		t.Context(),
		[]string{"custom-scope"},
		`{"access_token":{"essential":true}}`,
		&LoginInteractiveOptions{
			RedirectPort: 8080,
			TenantID:     "tenant-xyz",
			WithOpenUrl: func(url string) error {
				openURLCalled = true
				return nil
			},
		})
	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)
	// WithOpenUrl is appended but not invoked by LoginInteractive
	// itself — it's passed through to the MSAL library.
	_ = openURLCalled
}

func TestLoginInteractive_Error(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		publicClient: &mockPublicClientFull{
			interactiveErr: errWave3Test,
		},
	}

	_, err := m.LoginInteractive(t.Context(), nil, "", nil)
	require.ErrorIs(t, err, errWave3Test)
}

func TestLoginInteractive_WithSavedClaims(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	// Save claims on disk, then call LoginInteractive with
	// empty claims param so it loads from disk.
	require.NoError(t, saveClaims(
		`{"access_token":{"essential":true}}`))

	homeID := "claims-test"
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		publicClient: &mockPublicClientFull{
			interactiveRes: public.AuthResult{
				Account: public.Account{
					HomeAccountID: homeID,
				},
			},
		},
	}

	cred, err := m.LoginInteractive(t.Context(), nil, "", nil)
	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	// The claims file should have been removed after login.
	fp, _ := claimsFilePath()
	_, statErr := os.Stat(fp)
	require.True(t, os.IsNotExist(statErr))
}

// ---------- LoginWithDeviceCode — additional branches ----------

func TestLoginWithDeviceCode_WithClaims(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	homeID := "dc-claims"
	console := mockinput.NewMockConsole()
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		console:           console,
		publicClient: &mockPublicClientFull{
			deviceCodeResult: &stubDeviceCode{
				msg:  "go to device login",
				code: "ABC-123",
				res: public.AuthResult{
					Account: public.Account{
						HomeAccountID: homeID,
					},
				},
			},
		},
	}

	cred, err := m.LoginWithDeviceCode(
		t.Context(), "tenant-1", []string{"s1"},
		`{"access_token":{"essential":true}}`,
		func(_ string) error { return nil })
	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)
}

func TestLoginWithDeviceCode_OpenURLError(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	homeID := "dc-open-err"
	console := mockinput.NewMockConsole()
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		console:           console,
		publicClient: &mockPublicClientFull{
			deviceCodeResult: &stubDeviceCode{
				msg:  "msg",
				code: "CODE",
				res: public.AuthResult{
					Account: public.Account{
						HomeAccountID: homeID,
					},
				},
			},
		},
	}

	// withOpenUrl returns error — code should log it and continue.
	cred, err := m.LoginWithDeviceCode(
		t.Context(), "", nil, "",
		func(_ string) error { return errWave3Test })
	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)
	require.Contains(t, console.Output(),
		"Error launching browser. Manually go to: https://microsoft.com/devicelogin")
}

func TestLoginWithDeviceCode_AcquireError(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	console := mockinput.NewMockConsole()
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		console:           console,
		publicClient: &mockPublicClientFull{
			deviceCodeErr: errWave3Test,
		},
	}

	_, err := m.LoginWithDeviceCode(
		t.Context(), "", nil, "",
		func(_ string) error { return nil })
	require.ErrorIs(t, err, errWave3Test)
}

func TestLoginWithDeviceCode_AuthResultError(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	console := mockinput.NewMockConsole()
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		console:           console,
		publicClient: &mockPublicClientFull{
			deviceCodeResult: &stubDeviceCode{
				msg:  "msg",
				code: "CODE",
				err:  errWave3Test,
			},
		},
	}

	_, err := m.LoginWithDeviceCode(
		t.Context(), "", nil, "",
		func(_ string) error { return nil })
	require.ErrorIs(t, err, errWave3Test)
}

func TestLoginWithDeviceCode_SavedClaims(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	require.NoError(t, saveClaims(
		`{"access_token":{"essential":true}}`))

	homeID := "dc-saved"
	console := mockinput.NewMockConsole()
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		console:           console,
		publicClient: &mockPublicClientFull{
			deviceCodeResult: &stubDeviceCode{
				msg:  "msg",
				code: "CODE",
				res: public.AuthResult{
					Account: public.Account{
						HomeAccountID: homeID,
					},
				},
			},
		},
	}

	cred, err := m.LoginWithDeviceCode(
		t.Context(), "", nil, "",
		func(_ string) error { return nil })
	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	// Claims file should have been removed.
	fp, _ := claimsFilePath()
	_, statErr := os.Stat(fp)
	require.True(t, os.IsNotExist(statErr))
}

// ---------- CloseIdleConnections ----------

func TestUserAgentClient_CloseIdleConnections(t *testing.T) {
	t.Parallel()
	c := newUserAgentClient(http.DefaultClient, "ua")
	c.CloseIdleConnections()
}

// ---------- readAuthConfig migration path ----------

func TestReadAuthConfig_MigratesFromUserConfig(t *testing.T) {
	// Set up memoryConfigManager that returns ErrNotExist for
	// the auth config path, and a memoryUserConfigManager with
	// the currentUser data in the old location.
	cfgMgr := newMemoryConfigManager()
	userCfg := config.NewEmptyConfig()
	homeID := "migrated-id"
	require.NoError(t, userCfg.Set(currentUserKey, userProperties{
		HomeAccountID: &homeID,
	}))
	userCfgMgr := &memoryUserConfigManager{
		config: userCfg,
	}

	m := &Manager{
		configManager:     cfgMgr,
		userConfigManager: userCfgMgr,
		cloud:             cloud.AzurePublic(),
	}

	cfg, err := m.readAuthConfig()
	require.NoError(t, err)

	// The migrated config should have the user data.
	user, err := readUserProperties(cfg)
	require.NoError(t, err)
	require.Equal(t, homeID, *user.HomeAccountID)

	// The old user config should have had the key removed.
	_, has := userCfgMgr.config.Get(currentUserKey)
	require.False(t, has, "currentUser should be removed from old location")
}

// ---------- CredentialForCurrentUser — ManagedIdentity path ----------

func TestCredentialForCurrentUser_ManagedIdentity(t *testing.T) {
	cfgMgr := newMemoryConfigManager()
	userCfgMgr := newMemoryUserConfigManager()
	c := cloud.AzurePublic()

	clientID := "mi-client-id"
	m := &Manager{
		configManager:     cfgMgr,
		userConfigManager: userCfgMgr,
		cloud:             c,
		publicClient:      &mockPublicClientFull{},
	}
	require.NoError(t, m.saveUserProperties(&userProperties{
		ManagedIdentity: true,
		ClientID:        &clientID,
	}))

	cred, err := m.CredentialForCurrentUser(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, cred)
}

// ---------- CredentialForCurrentUser — SP with TenantID override --------

func TestCredentialForCurrentUser_SPTenantIDOverride(t *testing.T) {
	cfgMgr := newMemoryConfigManager()
	userCfgMgr := newMemoryUserConfigManager()
	c := cloud.AzurePublic()
	cache := &memoryCache{cache: map[string][]byte{}}

	clientID := "sp-client"
	tenantID := "sp-tenant"
	secret := "sp-secret"

	m := &Manager{
		configManager:     cfgMgr,
		userConfigManager: userCfgMgr,
		cloud:             c,
		credentialCache:   cache,
		publicClient:      &mockPublicClientFull{},
	}

	// Save SP login.
	require.NoError(t, m.saveLoginForServicePrincipal(
		tenantID, clientID, &persistedSecret{ClientSecret: &secret}))

	// Request with a different tenant.
	cred, err := m.CredentialForCurrentUser(t.Context(),
		&CredentialForCurrentUserOptions{TenantID: "override-tenant"})
	require.NoError(t, err)
	require.NotNil(t, cred)
}

// ---------- CredentialForCurrentUser — Public client with TenantID ------

func TestCredentialForCurrentUser_PublicClientTenantOverride(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	cfgMgr := newMemoryConfigManager()
	userCfgMgr := newMemoryUserConfigManager()
	c := cloud.AzurePublic()
	homeID := "tenant-override-user"

	mgr, err := NewManager(
		cfgMgr, userCfgMgr, c,
		http.DefaultClient, nil,
		ExternalAuthConfiguration{}, az.AzCli{}, "test-ua",
	)
	require.NoError(t, err)

	// Swap the public client with our mock after construction,
	// so that NewManager's real MSAL setup succeeds.
	mgr.publicClient = &mockPublicClientFull{
		accounts: []public.Account{
			{HomeAccountID: homeID},
		},
		silentRes: public.AuthResult{
			Account: public.Account{HomeAccountID: homeID},
		},
	}

	// Save login.
	require.NoError(t, mgr.saveUserProperties(
		&userProperties{HomeAccountID: &homeID}))

	cred, err := mgr.CredentialForCurrentUser(t.Context(),
		&CredentialForCurrentUserOptions{TenantID: "other-tenant"})
	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)
}

// ---------- getSignedInAccount — account found vs not found ----------

func TestGetSignedInAccount_Found(t *testing.T) {
	homeID := "acct-found"
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		publicClient: &mockPublicClientFull{
			accounts: []public.Account{
				{HomeAccountID: homeID},
			},
		},
	}
	require.NoError(t, m.saveUserProperties(
		&userProperties{HomeAccountID: &homeID}))

	acct, err := m.getSignedInAccount(t.Context())
	require.NoError(t, err)
	require.NotNil(t, acct)
	require.Equal(t, homeID, acct.HomeAccountID)
}

func TestGetSignedInAccount_NotInAccountsList(t *testing.T) {
	homeID := "acct-missing"
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		publicClient: &mockPublicClientFull{
			accounts: []public.Account{}, // empty
		},
	}
	require.NoError(t, m.saveUserProperties(
		&userProperties{HomeAccountID: &homeID}))

	acct, err := m.getSignedInAccount(t.Context())
	require.NoError(t, err)
	require.Nil(t, acct)
}

func TestGetSignedInAccount_SPLogin(t *testing.T) {
	clientID := "sp-cli"
	tenantID := "sp-tid"
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		credentialCache:   &memoryCache{cache: map[string][]byte{}},
		publicClient:      &mockPublicClientFull{},
	}
	// Log in as SP — no HomeAccountID.
	require.NoError(t, m.saveLoginForServicePrincipal(
		tenantID, clientID,
		&persistedSecret{ClientSecret: new(string)}))

	acct, err := m.getSignedInAccount(t.Context())
	require.NoError(t, err)
	require.Nil(t, acct, "SP login should not return an account")
}

// ---------- Logout — additional branches ----------

func TestLogout_WhenPublicClientRemoveErrors(t *testing.T) {
	homeID := "logout-remove-err"
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		publicClient: &mockPublicClientFull{
			accounts: []public.Account{
				{HomeAccountID: homeID},
			},
			removeErr: errWave3Test,
		},
	}
	require.NoError(t, m.saveUserProperties(
		&userProperties{HomeAccountID: &homeID}))

	err := m.Logout(t.Context())
	require.ErrorIs(t, err, errWave3Test)
}

// ---------- LogInDetails — userConfigManager load error ----------

// failingUserConfigManager always returns an error from Load.
type failingUserConfigManager struct {
	err error
}

func (f *failingUserConfigManager) Load() (config.Config, error) {
	return nil, f.err
}

func (f *failingUserConfigManager) Save(_ config.Config) error {
	return f.err
}

func TestLogInDetails_UserConfigLoadError(t *testing.T) {
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: &failingUserConfigManager{err: errWave3Test},
		cloud:             cloud.AzurePublic(),
	}

	_, err := m.LogInDetails(t.Context())
	require.Error(t, err)
}

// ---------- saveSecret / loadSecret round-trip ----------

func TestSaveAndLoadSecret(t *testing.T) {
	cache := &memoryCache{cache: map[string][]byte{}}
	secret := "s3cret"
	m := &Manager{credentialCache: cache}

	require.NoError(t, m.saveSecret("tid", "cid",
		&persistedSecret{ClientSecret: &secret}))

	ps, err := m.loadSecret("tid", "cid")
	require.NoError(t, err)
	require.Equal(t, secret, *ps.ClientSecret)
}

func TestLoadSecret_BadJSON(t *testing.T) {
	cache := &memoryCache{
		cache: map[string][]byte{
			persistedSecretLookupKey("tid", "cid"): []byte("not-json"),
		},
	}
	m := &Manager{credentialCache: cache}

	_, err := m.loadSecret("tid", "cid")
	require.Error(t, err)
}

// ---------- saveAuthConfig / readAuthConfig round-trip ----------

func TestSaveAndReadAuthConfig(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	cfgMgr := config.NewFileConfigManager(
		config.NewManager())
	m := &Manager{
		configManager:     cfgMgr,
		userConfigManager: newMemoryUserConfigManager(),
	}

	// Save.
	authCfg := config.NewEmptyConfig()
	homeID := "round-trip"
	require.NoError(t, authCfg.Set(currentUserKey, userProperties{
		HomeAccountID: &homeID,
	}))
	require.NoError(t, m.saveAuthConfig(authCfg))

	// Read back.
	got, err := m.readAuthConfig()
	require.NoError(t, err)
	user, err := readUserProperties(got)
	require.NoError(t, err)
	require.Equal(t, homeID, *user.HomeAccountID)
}

// ---------- LoginWithServicePrincipalCertificate — error paths ----------

func TestLoginWithSPCertificate_BadCert(t *testing.T) {
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		credentialCache:   &memoryCache{cache: map[string][]byte{}},
	}

	_, err := m.LoginWithServicePrincipalCertificate(
		t.Context(), "tid", "cid", []byte("not-a-cert"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing certificate")
}

// ---------- GetLoggedInServicePrincipalTenantID — tracing paths ---------

func TestGetLoggedInSPTenantID_BothTracingBranches(t *testing.T) {
	// SP path sets tracing for AccountTypeServicePrincipal.
	tenantID := "trace-tid"
	clientID := "trace-cid"
	secret := "s"
	cache := &memoryCache{cache: map[string][]byte{}}
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		credentialCache:   cache,
		publicClient:      &mockPublicClientFull{},
	}
	require.NoError(t, m.saveLoginForServicePrincipal(
		tenantID, clientID,
		&persistedSecret{ClientSecret: &secret}))

	tid, err := m.GetLoggedInServicePrincipalTenantID(t.Context())
	require.NoError(t, err)
	require.NotNil(t, tid)
	require.Equal(t, tenantID, *tid)

	// User path sets tracing for AccountTypeUser.
	m2 := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		publicClient:      &mockPublicClientFull{},
	}
	homeID := "trace-home"
	require.NoError(t, m2.saveUserProperties(
		&userProperties{HomeAccountID: &homeID}))

	tid2, err := m2.GetLoggedInServicePrincipalTenantID(t.Context())
	require.NoError(t, err)
	require.Nil(t, tid2, "user login has nil TenantID")
}

// ---------- CredentialForCurrentUser — legacy auth path -----------------

func TestCredentialForCurrentUser_LegacyAuth(t *testing.T) {
	cfgMgr := newMemoryConfigManager()
	userCfg := config.NewEmptyConfig()
	require.NoError(t, userCfg.Set(useAzCliAuthKey, "true"))
	userCfgMgr := &memoryUserConfigManager{config: userCfg}

	m := &Manager{
		configManager:     cfgMgr,
		userConfigManager: userCfgMgr,
		cloud:             cloud.AzurePublic(),
		publicClient:      &mockPublicClientFull{},
	}

	// The legacy path calls azidentity.NewAzureCLICredential
	// which should succeed (it doesn't verify the credential
	// until GetToken is called).
	cred, err := m.CredentialForCurrentUser(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, cred)
}

// ---------- saveLoginForPublicClient / saveUserProperties error ---------

func TestSaveLoginForPublicClient_SavesUser(t *testing.T) {
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
	}

	homeID := "save-pub"
	err := m.saveLoginForPublicClient(public.AuthResult{
		Account: public.Account{HomeAccountID: homeID},
	})
	require.NoError(t, err)

	cfg, err := m.readAuthConfig()
	require.NoError(t, err)
	user, err := readUserProperties(cfg)
	require.NoError(t, err)
	require.Equal(t, homeID, *user.HomeAccountID)
}

// ---------- credential cache file path ----------

func TestCredentialCachePaths(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	mgr, err := NewManager(
		newMemoryConfigManager(),
		newMemoryUserConfigManager(),
		cloud.AzurePublic(),
		http.DefaultClient,
		nil,
		ExternalAuthConfiguration{},
		az.AzCli{},
		"ua",
	)
	require.NoError(t, err)

	// Verify the auth and msal cache directories were created.
	cfgDir := os.Getenv("AZD_CONFIG_DIR")
	authDir := filepath.Join(cfgDir, "auth")
	msalDir := filepath.Join(authDir, "msal")

	info, err := os.Stat(authDir) //nolint:gosec // G703: path is from AZD_CONFIG_DIR test env var
	require.NoError(t, err)
	require.True(t, info.IsDir())

	info, err = os.Stat(msalDir) //nolint:gosec // G703: path is from AZD_CONFIG_DIR test env var
	require.NoError(t, err)
	require.True(t, info.IsDir())
	_ = mgr
}

// ---------- Errors — NonRetriable coverage (if not hit) -----------------
// These exercise the marker methods directly.

func TestNonRetriable_AuthFailed(t *testing.T) {
	t.Parallel()
	e := &AuthFailedError{innerErr: errWave3Test}
	e.NonRetriable() // should not panic
}

func TestNonRetriable_ReLoginRequired(t *testing.T) {
	t.Parallel()
	e := &ReLoginRequiredError{}
	e.NonRetriable() // should not panic
}
