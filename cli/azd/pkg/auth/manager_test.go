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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestReadUserProperties(t *testing.T) {
	t.Run("homeID", func(t *testing.T) {
		cfg := config.NewConfig(nil)
		require.NoError(t, cfg.Set("auth.account.currentUser.homeAccountId", "testAccountId"))

		props, err := readUserProperties(cfg)

		require.NoError(t, err)
		require.Nil(t, props.ClientID)
		require.Nil(t, props.TenantID)
		require.Equal(t, "testAccountId", *props.HomeAccountID)
	})

	t.Run("clientID", func(t *testing.T) {
		cfg := config.NewConfig(nil)
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
		configManager:   &memoryConfigManager{},
		credentialCache: credentialCache,
	}

	cred, err := m.LoginWithServicePrincipalSecret(
		context.Background(), "testClientId", "testTenantId", "testClientSecret",
	)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientSecretCredential), cred)

	cred, err = m.CredentialForCurrentUser(context.Background())

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientSecretCredential), cred)

	err = m.Logout(context.Background())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(context.Background())

	require.True(t, errors.Is(err, ErrNoCurrentUser))
}

//go:embed testdata/certificate.pem
var cTestClientCertificate []byte

func TestServicePrincipalLoginClientCertificate(t *testing.T) {
	credentialCache := &memoryCache{
		cache: make(map[string][]byte),
	}

	m := Manager{
		configManager:   &memoryConfigManager{},
		credentialCache: credentialCache,
	}

	cred, err := m.LoginWithServicePrincipalCertificate(
		context.Background(), "testClientId", "testTenantId", cTestClientCertificate,
	)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientCertificateCredential), cred)

	cred, err = m.CredentialForCurrentUser(context.Background())

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientCertificateCredential), cred)

	err = m.Logout(context.Background())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(context.Background())

	require.True(t, errors.Is(err, ErrNoCurrentUser))
}

func TestServicePrincipalLoginFederatedToken(t *testing.T) {
	credentialCache := &memoryCache{
		cache: make(map[string][]byte),
	}

	m := Manager{
		configManager:   &memoryConfigManager{},
		credentialCache: credentialCache,
	}

	cred, err := m.LoginWithServicePrincipalFederatedToken(
		context.Background(), "testClientId", "testTenantId", "testToken",
	)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientAssertionCredential), cred)

	cred, err = m.CredentialForCurrentUser(context.Background())

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientAssertionCredential), cred)

	err = m.Logout(context.Background())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(context.Background())

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
		configManager:   &memoryConfigManager{},
		credentialCache: credentialCache,
		ghClient: newGitHubFederatedTokenClient(&policy.ClientOptions{
			Transport: mockContext.HttpClient,
		}),
	}

	cred, err := m.LoginWithServicePrincipalFederatedTokenProvider(
		context.Background(), "testClientId", "testTenantId", "github",
	)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientAssertionCredential), cred)

	cred, err = m.CredentialForCurrentUser(context.Background())

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientAssertionCredential), cred)

	err = m.Logout(context.Background())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(context.Background())

	require.True(t, errors.Is(err, ErrNoCurrentUser))
}

func TestLegacyAzCliCredentialSupport(t *testing.T) {
	mgr := &memoryConfigManager{
		configs: make(map[string]config.Config),
	}

	cfg := config.NewConfig(nil)
	err := cfg.Set(cUseLegacyAzCliAuthKey, "true")

	require.NoError(t, err)

	path, err := config.GetUserConfigFilePath()
	require.NoError(t, err)

	mgr.configs[path] = cfg

	m := Manager{
		configManager: mgr,
	}

	cred, err := m.CredentialForCurrentUser(context.Background())

	require.NoError(t, err)
	require.IsType(t, new(azidentity.AzureCLICredential), cred)

}

func TestLoginInteractive(t *testing.T) {
	m := &Manager{
		configManager: &memoryConfigManager{
			configs: make(map[string]config.Config),
		},
		publicClient: &mockPublicClient{},
	}

	cred, err := m.LoginInteractive(context.Background())

	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	cred, err = m.CredentialForCurrentUser(context.Background())

	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	err = m.Logout(context.Background())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(context.Background())

	require.True(t, errors.Is(err, ErrNoCurrentUser))
}

func TestLoginDeviceCode(t *testing.T) {
	m := &Manager{
		configManager: &memoryConfigManager{
			configs: make(map[string]config.Config),
		},
		publicClient: &mockPublicClient{},
	}

	buf := bytes.Buffer{}

	cred, err := m.LoginWithDeviceCode(context.Background(), &buf)

	require.Regexp(t, "using the code 123-456", buf.String())

	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	cred, err = m.CredentialForCurrentUser(context.Background())

	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	err = m.Logout(context.Background())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(context.Background())

	require.True(t, errors.Is(err, ErrNoCurrentUser))
}

type memoryConfigManager struct {
	configs map[string]config.Config
}

func (m *memoryConfigManager) Load(filePath string) (config.Config, error) {
	if cfg, has := m.configs[filePath]; has {
		return cfg, nil
	}

	return nil, os.ErrNotExist
}

func (m *memoryConfigManager) Save(cfg config.Config, filePath string) error {
	if m.configs == nil {
		m.configs = make(map[string]config.Config)
	}

	m.configs[filePath] = cfg
	return nil
}

type mockPublicClient struct {
}

func (m *mockPublicClient) Accounts() []public.Account {
	return []public.Account{
		{
			HomeAccountID: "test.id",
		},
	}
}

func (m *mockPublicClient) RemoveAccount(account public.Account) error {
	return nil
}

func (m *mockPublicClient) AcquireTokenInteractive(
	ctx context.Context, scopes []string, options ...public.InteractiveAuthOption,
) (public.AuthResult, error) {
	return public.AuthResult{
		Account: public.Account{
			HomeAccountID: "test.id",
		},
	}, nil
}

func (m *mockPublicClient) AcquireTokenSilent(
	ctx context.Context, scopes []string, options ...public.AcquireTokenSilentOption,
) (public.AuthResult, error) {
	return public.AuthResult{
		Account: public.Account{
			HomeAccountID: "test.id",
		},
	}, nil
}

func (m *mockPublicClient) AcquireTokenByDeviceCode(ctx context.Context, scopes []string) (deviceCodeResult, error) {
	return &mockDeviceCode{}, nil
}

type mockDeviceCode struct {
}

func (m *mockDeviceCode) Message() string {
	return "Complete the device code flow on your second device using the code 123-456"
}

func (m *mockDeviceCode) AuthenticationResult(ctx context.Context) (public.AuthResult, error) {
	return public.AuthResult{
		Account: public.Account{
			HomeAccountID: "test.id",
		},
	}, nil
}
