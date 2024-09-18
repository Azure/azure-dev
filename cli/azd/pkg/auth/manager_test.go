// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"

	_ "embed"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/internal/runcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/github"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestReadUserProperties(t *testing.T) {
	t.Run("homeID", func(t *testing.T) {
		cfg := config.NewEmptyConfig()
		require.NoError(t, cfg.Set("auth.account.currentUser.homeAccountId", "testAccountId"))

		props, err := readUserProperties(cfg)

		require.NoError(t, err)
		require.Nil(t, props.ClientID)
		require.Nil(t, props.TenantID)
		require.Equal(t, "testAccountId", *props.HomeAccountID)
	})

	t.Run("clientID", func(t *testing.T) {
		cfg := config.NewEmptyConfig()
		require.NoError(t, cfg.Set("auth.account.currentUser.clientId", "testClientId"))
		require.NoError(t, cfg.Set("auth.account.currentUser.tenantId", "testTenantId"))

		props, err := readUserProperties(cfg)

		require.NoError(t, err)
		require.Nil(t, props.HomeAccountID)
		require.Equal(t, "testClientId", *props.ClientID)
		require.Equal(t, "testTenantId", *props.TenantID)
	})
}

func TestServicePrincipalLoginClientSecret(t *testing.T) {
	credentialCache := &memoryCache{
		cache: make(map[string][]byte),
	}

	m := Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		credentialCache:   credentialCache,
		cloud:             cloud.AzurePublic(),
	}

	cred, err := m.LoginWithServicePrincipalSecret(
		context.Background(), "testClientId", "testTenantId", "testClientSecret",
	)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientSecretCredential), cred)

	cred, err = m.CredentialForCurrentUser(context.Background(), nil)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientSecretCredential), cred)

	err = m.Logout(context.Background())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(context.Background(), nil)

	require.True(t, errors.Is(err, ErrNoCurrentUser))
}

//go:embed testdata/certificate.pem
var testClientCertificate []byte

func TestServicePrincipalLoginClientCertificate(t *testing.T) {
	credentialCache := &memoryCache{
		cache: make(map[string][]byte),
	}

	m := Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		credentialCache:   credentialCache,
		cloud:             cloud.AzurePublic(),
	}

	cred, err := m.LoginWithServicePrincipalCertificate(
		context.Background(), "testClientId", "testTenantId", testClientCertificate,
	)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientCertificateCredential), cred)

	cred, err = m.CredentialForCurrentUser(context.Background(), nil)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientCertificateCredential), cred)

	err = m.Logout(context.Background())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(context.Background(), nil)

	require.True(t, errors.Is(err, ErrNoCurrentUser))
}

func TestServicePrincipalLoginFederatedTokenProvider(t *testing.T) {
	credentialCache := &memoryCache{
		cache: make(map[string][]byte),
	}

	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "http://fakehost/api/get-token")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "fake-token")

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return true
	}).Respond(&http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewBufferString(`{ "value": "abc" }`)),
	})

	m := Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		credentialCache:   credentialCache,
		ghClient: github.NewFederatedTokenClient(&azcore.ClientOptions{
			Transport: mockContext.HttpClient,
			Cloud:     cloud.AzurePublic().Configuration,
		}),
		cloud: cloud.AzurePublic(),
	}

	cred, err := m.LoginWithGitHubFederatedTokenProvider(context.Background(), "testClientId", "testTenantId")

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientAssertionCredential), cred)

	cred, err = m.CredentialForCurrentUser(context.Background(), nil)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientAssertionCredential), cred)

	err = m.Logout(context.Background())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(context.Background(), nil)

	require.True(t, errors.Is(err, ErrNoCurrentUser))
}

func TestLegacyAzCliCredentialSupport(t *testing.T) {
	mgr := newMemoryUserConfigManager()

	cfg, err := mgr.Load()
	require.NoError(t, err)

	err = cfg.Set(useAzCliAuthKey, "true")
	require.NoError(t, err)

	err = mgr.Save(cfg)
	require.NoError(t, err)

	m := Manager{
		userConfigManager: mgr,
	}

	cred, err := m.CredentialForCurrentUser(context.Background(), nil)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.AzureCLICredential), cred)
}

func TestCloudShellCredentialSupport(t *testing.T) {
	t.Setenv(runcontext.AzdInCloudShellEnvVar, "1")
	m := Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
	}

	cred, err := m.CredentialForCurrentUser(context.Background(), nil)
	require.NoError(t, err)
	require.IsType(t, new(CloudShellCredential), cred)
}

func TestLoginInteractive(t *testing.T) {
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient:      &mockPublicClient{},
		cloud:             cloud.AzurePublic(),
	}

	cred, err := m.LoginInteractive(context.Background(), nil, nil)

	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	cred, err = m.CredentialForCurrentUser(context.Background(), nil)

	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	err = m.Logout(context.Background())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(context.Background(), nil)

	require.True(t, errors.Is(err, ErrNoCurrentUser))
}

func TestLoginDeviceCode(t *testing.T) {
	console := mockinput.NewMockConsole()
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient:      &mockPublicClient{},
		console:           console,
		cloud:             cloud.AzurePublic(),
	}

	cred, err := m.LoginWithDeviceCode(context.Background(), "", nil, func(url string) error { return nil })

	require.Regexp(t, "Start by copying the next code: 123-456", console.Output())

	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	cred, err = m.CredentialForCurrentUser(context.Background(), nil)

	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	err = m.Logout(context.Background())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(context.Background(), nil)

	require.True(t, errors.Is(err, ErrNoCurrentUser))
}

func TestAuthFileConfigUpgrade(t *testing.T) {
	cfgMgr := newMemoryConfigManager()
	userCfg := config.NewEmptyConfig()
	userCfgMgr := newMemoryUserConfigManager()

	err := userCfg.Set(currentUserKey, &userProperties{
		HomeAccountID: to.Ptr("homeAccountID"),
	})
	require.NoError(t, err)

	err = userCfgMgr.Save(userCfg)
	require.NoError(t, err)

	m := &Manager{
		configManager:     cfgMgr,
		userConfigManager: userCfgMgr,
		publicClient:      &mockPublicClient{},
	}

	cfg, err := m.readAuthConfig()
	require.NoError(t, err)

	properties, err := readUserProperties(cfg)
	require.NoError(t, err)
	require.NotNil(t, properties.HomeAccountID)
	require.Equal(t, "homeAccountID", *properties.HomeAccountID)

	// as part of running readAuthConfig, we migrated the setting from the user config to the auth config
	// so the current user key should no longer be set in the user configuration.
	_, has := userCfgMgr.config.Get(currentUserKey)
	require.False(t, has)
}

func newMemoryUserConfigManager() *memoryUserConfigManager {
	return &memoryUserConfigManager{
		config: config.NewEmptyConfig(),
	}
}

type memoryUserConfigManager struct {
	config config.Config
}

func (m *memoryUserConfigManager) Load() (config.Config, error) {
	return m.config, nil
}

func (m *memoryUserConfigManager) Save(cfg config.Config) error {
	m.config = cfg
	return nil
}

func newMemoryConfigManager() *memoryConfigManager {
	return &memoryConfigManager{
		configs: map[string]config.Config{},
	}
}

type memoryConfigManager struct {
	configs map[string]config.Config
}

func (m *memoryConfigManager) Load(path string) (config.Config, error) {
	c, has := m.configs[path]
	if !has {
		return nil, os.ErrNotExist
	}
	return c, nil
}

func (m *memoryConfigManager) Save(cfg config.Config, path string) error {
	m.configs[path] = cfg
	return nil
}

type mockPublicClient struct {
}

func (m *mockPublicClient) Accounts(ctx context.Context) ([]public.Account, error) {
	return []public.Account{
		{
			HomeAccountID: "test.id",
		},
	}, nil
}

func (m *mockPublicClient) RemoveAccount(ctx context.Context, account public.Account) error {
	return nil
}

func (m *mockPublicClient) AcquireTokenInteractive(
	ctx context.Context, scopes []string, options ...public.AcquireInteractiveOption,
) (public.AuthResult, error) {
	return public.AuthResult{
		Account: public.Account{
			HomeAccountID: "test.id",
		},
	}, nil
}

func (m *mockPublicClient) AcquireTokenSilent(
	ctx context.Context, scopes []string, options ...public.AcquireSilentOption,
) (public.AuthResult, error) {
	return public.AuthResult{
		Account: public.Account{
			HomeAccountID: "test.id",
		},
	}, nil
}

func (m *mockPublicClient) AcquireTokenByDeviceCode(
	ctx context.Context, scopes []string, options ...public.AcquireByDeviceCodeOption) (deviceCodeResult, error) {
	return &mockDeviceCode{}, nil
}

type mockDeviceCode struct {
}

func (m *mockDeviceCode) Message() string {
	return "Complete the device code flow on your second device using the code 123-456"
}

func (m *mockDeviceCode) UserCode() string {
	return "123-456"
}

func (m *mockDeviceCode) AuthenticationResult(ctx context.Context) (public.AuthResult, error) {
	return public.AuthResult{
		Account: public.Account{
			HomeAccountID: "test.id",
		},
	}, nil
}
