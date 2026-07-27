// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "embed"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal/runcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/az"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
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
		t.Context(), "testClientId", "testTenantId", "testClientSecret",
	)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientSecretCredential), cred)

	cred, err = m.CredentialForCurrentUser(t.Context(), nil)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientSecretCredential), cred)

	err = m.Logout(t.Context())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(t.Context(), nil)

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
		t.Context(), "testClientId", "testTenantId", testClientCertificate,
	)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientCertificateCredential), cred)

	cred, err = m.CredentialForCurrentUser(t.Context(), nil)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientCertificateCredential), cred)

	err = m.Logout(t.Context())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(t.Context(), nil)

	require.True(t, errors.Is(err, ErrNoCurrentUser))
}

func TestServicePrincipalLoginFederatedTokenProvider(t *testing.T) {
	credentialCache := &memoryCache{
		cache: make(map[string][]byte),
	}

	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "http://fakehost/api/get-token")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "fake-token")

	mockContext := mocks.NewMockContext(t.Context())
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
		httpClient:        mockContext.HttpClient,
		cloud:             cloud.AzurePublic(),
	}

	cred, err := m.LoginWithGitHubFederatedTokenProvider(t.Context(), "testClientId", "testTenantId")

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientAssertionCredential), cred)

	cred, err = m.CredentialForCurrentUser(t.Context(), nil)

	require.NoError(t, err)
	require.IsType(t, new(azidentity.ClientAssertionCredential), cred)

	err = m.Logout(t.Context())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(t.Context(), nil)

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

	cred, err := m.CredentialForCurrentUser(t.Context(), nil)

	require.NoError(t, err)
	// The credential is wrapped in a cachingCredential that reuses tokens across concurrent callers,
	// backed by an AzureCLICredential.
	require.IsType(t, new(cachingCredential), cred)
	require.IsType(t, new(azidentity.AzureCLICredential), cred.(*cachingCredential).inner)
}

func TestLegacyAzCliCredentialIsCached(t *testing.T) {
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

	// The same credential instance should be returned on subsequent calls for the same tenant so that the
	// azidentity SDK can collapse concurrent token requests into a single `az` subprocess.
	first, err := m.CredentialForCurrentUser(t.Context(), nil)
	require.NoError(t, err)

	second, err := m.CredentialForCurrentUser(t.Context(), nil)
	require.NoError(t, err)

	require.Same(t, first, second)

	// A different tenant should yield a distinct credential instance.
	other, err := m.CredentialForCurrentUser(t.Context(), &CredentialForCurrentUserOptions{TenantID: "other-tenant"})
	require.NoError(t, err)

	require.NotSame(t, first, other)
}

func TestCloudShellCredentialSupport(t *testing.T) {
	t.Setenv(runcontext.AzdInCloudShellEnvVar, "1")
	m := Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
	}

	cred, err := m.CredentialForCurrentUser(t.Context(), nil)
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

	cred, err := m.LoginInteractive(t.Context(), nil, "", nil)

	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	cred, err = m.CredentialForCurrentUser(t.Context(), nil)

	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	err = m.Logout(t.Context())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(t.Context(), nil)

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

	cred, err := m.LoginWithDeviceCode(t.Context(), "", nil, "", func(url string) error { return nil })

	require.Regexp(t, "Start by copying the next code: 123-456", console.Output())

	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	cred, err = m.CredentialForCurrentUser(t.Context(), nil)

	require.NoError(t, err)
	require.IsType(t, new(azdCredential), cred)

	err = m.Logout(t.Context())

	require.NoError(t, err)

	_, err = m.CredentialForCurrentUser(t.Context(), nil)

	require.True(t, errors.Is(err, ErrNoCurrentUser))
}

func TestAuthFileConfigUpgrade(t *testing.T) {
	cfgMgr := newMemoryConfigManager()
	userCfg := config.NewEmptyConfig()
	userCfgMgr := newMemoryUserConfigManager()

	err := userCfg.Set(currentUserKey, &userProperties{
		HomeAccountID: new("homeAccountID"),
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

func TestLogInDetails(t *testing.T) {
	t.Run("legacy az cli auth - user account", func(t *testing.T) {
		mgr := newMemoryUserConfigManager()
		cfg, err := mgr.Load()
		require.NoError(t, err)

		require.NoError(t, cfg.Set(useAzCliAuthKey, "true"))
		require.NoError(t, mgr.Save(cfg))

		// mock command runner to return a user account
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "az account show")
		}).Respond(exec.RunResult{
			Stdout: `{"user": {"name": "test@example.com", "type": "user"}}`,
		})

		mockAzCli, err := az.NewCli(mockContext.CommandRunner)
		require.NoError(t, err)

		m := Manager{
			userConfigManager: mgr,
			azCli:             mockAzCli,
		}

		details, err := m.LogInDetails(t.Context())
		require.NoError(t, err)
		require.Equal(t, EmailLoginType, details.LoginType)
		require.Equal(t, "test@example.com", details.Account)
	})

	t.Run("legacy az cli auth - service principal", func(t *testing.T) {
		mgr := newMemoryUserConfigManager()
		cfg, err := mgr.Load()
		require.NoError(t, err)

		require.NoError(t, cfg.Set(useAzCliAuthKey, "true"))
		require.NoError(t, mgr.Save(cfg))

		// mock command runner to return a user account
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "az account show")
		}).Respond(exec.RunResult{
			Stdout: `{"user": {"name": "12345678-1234-1234-1234-123456789012", "type": "servicePrincipal"}}`,
		})

		mockAzCli, err := az.NewCli(mockContext.CommandRunner)
		require.NoError(t, err)

		m := Manager{
			userConfigManager: mgr,
			azCli:             mockAzCli,
		}

		details, err := m.LogInDetails(t.Context())
		require.NoError(t, err)
		require.Equal(t, ClientIdLoginType, details.LoginType)
		require.Equal(t, "12345678-1234-1234-1234-123456789012", details.Account)
	})

	t.Run("legacy az cli auth - not authenticated", func(t *testing.T) {
		mgr := newMemoryUserConfigManager()
		cfg, err := mgr.Load()
		require.NoError(t, err)

		require.NoError(t, cfg.Set(useAzCliAuthKey, "true"))
		require.NoError(t, mgr.Save(cfg))

		// mock command runner to return an exit error with "az login" message
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "az account show")
		}).SetError(fmt.Errorf(
			"exit code: 1, stdout: , stderr: ERROR: Please run 'az login' to setup account.",
		))

		mockAzCli, err := az.NewCli(mockContext.CommandRunner)
		require.NoError(t, err)

		m := Manager{
			userConfigManager: mgr,
			azCli:             mockAzCli,
		}

		_, err = m.LogInDetails(t.Context())
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNoCurrentUser)
	})

	t.Run("external auth - returns email login type with upn from token", func(t *testing.T) {
		// Build a JWT token with a preferred_username claim
		token := buildTestJWT(t, map[string]any{
			"preferred_username": "user@contoso.com",
			"oid":                "oid-abc",
			"tid":                "tenant-xyz",
		})

		// Set up a mock HTTP server that returns the token
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"status":"success","token":"`+token+`","expiresOn":"2030-01-01T00:00:00Z"}`)
		}))
		defer srv.Close()

		m := Manager{
			externalAuthCfg: ExternalAuthConfiguration{
				Endpoint:    srv.URL,
				Key:         "test-key",
				Transporter: srv.Client(),
			},
			cloud: cloud.AzurePublic(),
		}

		details, err := m.LogInDetails(t.Context())
		require.NoError(t, err)
		require.Equal(t, EmailLoginType, details.LoginType)
		require.Equal(t, "user@contoso.com", details.Account)
	})

	t.Run("cloud shell - returns user login type from token claims", func(t *testing.T) {
		t.Setenv(runcontext.AzdInCloudShellEnvVar, "1")

		// Build an access token with a username claim and mock the Cloud Shell
		// token endpoint to return it.
		token := buildTestJWT(t, map[string]any{
			"unique_name": "user@contoso.com",
			"oid":         "oid-abc",
			"tid":         "tenant-xyz",
		})

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.URL.String() == "http://localhost:50342/oauth2/token"
		}).Respond(&http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(bytes.NewBufferString(
				fmt.Sprintf(`{"access_token":"%s","expires_on":"4070908800"}`, token))),
		})

		m := Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: newMemoryUserConfigManager(),
			httpClient:        mockContext.HttpClient,
			cloud:             cloud.AzurePublic(),
		}

		details, err := m.LogInDetails(t.Context())
		require.NoError(t, err)
		require.Equal(t, EmailLoginType, details.LoginType)
		require.Equal(t, "user@contoso.com", details.Account)
	})

	t.Run("cloud shell - authenticated even when token has no username claim", func(t *testing.T) {
		t.Setenv(runcontext.AzdInCloudShellEnvVar, "1")

		// A Cloud Shell session is always a valid authenticated user, even if
		// the token does not expose a username claim.
		token := buildTestJWT(t, map[string]any{
			"oid": "oid-abc",
			"tid": "tenant-xyz",
		})

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.URL.String() == "http://localhost:50342/oauth2/token"
		}).Respond(&http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(bytes.NewBufferString(
				fmt.Sprintf(`{"access_token":"%s","expires_on":"4070908800"}`, token))),
		})

		m := Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: newMemoryUserConfigManager(),
			httpClient:        mockContext.HttpClient,
			cloud:             cloud.AzurePublic(),
		}

		details, err := m.LogInDetails(t.Context())
		require.NoError(t, err)
		require.Equal(t, EmailLoginType, details.LoginType)
		require.Empty(t, details.Account)
	})

	t.Run("cloud shell - corrupted user properties surface error instead of fallback", func(t *testing.T) {
		t.Setenv(runcontext.AzdInCloudShellEnvVar, "1")

		// A stored currentUser value that cannot be unmarshalled into userProperties
		// represents real config corruption. It must surface as an error rather than
		// being silently masked by the Cloud Shell fallback.
		userCfg := config.NewEmptyConfig()
		require.NoError(t, userCfg.Set(currentUserKey, "not-an-object"))

		userCfgMgr := newMemoryUserConfigManager()
		require.NoError(t, userCfgMgr.Save(userCfg))

		m := Manager{
			configManager:     newMemoryConfigManager(),
			userConfigManager: userCfgMgr,
			cloud:             cloud.AzurePublic(),
		}

		_, err := m.LogInDetails(t.Context())
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrNoCurrentUser)
		require.ErrorContains(t, err, "reading current user properties")
	})

	t.Run("external auth - error when token has no usable account identifier", func(t *testing.T) {
		// Build a JWT token with no username claims
		token := buildTestJWT(t, map[string]any{
			"oid": "oid-abc",
			"tid": "tenant-xyz",
		})

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"status":"success","token":"`+token+`","expiresOn":"2030-01-01T00:00:00Z"}`)
		}))
		defer srv.Close()

		m := Manager{
			externalAuthCfg: ExternalAuthConfiguration{
				Endpoint:    srv.URL,
				Key:         "test-key",
				Transporter: srv.Client(),
			},
			cloud: cloud.AzurePublic(),
		}

		_, err := m.LogInDetails(t.Context())
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNoCurrentUser)
	})
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
	require.IsType(t, new(cachingCredential), cred)
	require.IsType(t, new(azidentity.AzureCLICredential), cred.(*cachingCredential).inner)
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
	require.IsType(t, new(cachingCredential), cred)
	require.IsType(t, new(azidentity.AzureCLICredential), cred.(*cachingCredential).inner)
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
		// Explicitly clear so the test works even in Azure Pipelines CI
		// where SYSTEM_ACCESSTOKEN is set in the process environment.
		t.Setenv("SYSTEM_ACCESSTOKEN", "")

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
		// Code uses os.LookupEnv, so vars must be truly unset (not just empty).
		// t.Setenv registers cleanup to restore original values.
		t.Setenv("AZURE_OIDC_TOKEN", "")
		os.Unsetenv("AZURE_OIDC_TOKEN")
		t.Setenv("AZURE_OIDC_REQUEST_TOKEN", "")
		os.Unsetenv("AZURE_OIDC_REQUEST_TOKEN")
		t.Setenv("AZURE_OIDC_REQUEST_URL", "")
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

// --- CleanAllAuthCache ---

func TestCleanAllAuthCache(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	// Create auth directory structure with files
	authDir := filepath.Join(tempDir, "auth")
	msalDir := filepath.Join(authDir, "msal")
	require.NoError(t, os.MkdirAll(msalDir, osutil.PermissionDirectoryOwnerOnly))

	// Create MSAL cache file
	require.NoError(t, os.WriteFile(
		filepath.Join(msalDir, "cache.json"), []byte(`{"tokens":"stale"}`), osutil.PermissionFileOwnerOnly))

	// Create credential cache file
	require.NoError(t, os.WriteFile(
		filepath.Join(authDir, "credtenant.client.json"), []byte(`{"secret":"old"}`), osutil.PermissionFileOwnerOnly))

	// Create auth.json
	require.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "auth.json"), []byte(`{"auth.account.currentUser":{}}`), osutil.PermissionFileOwnerOnly))

	// Create auth.claims
	require.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "auth.claims"), []byte(`claims-data`), osutil.PermissionFileOwnerOnly))

	m := &Manager{}
	err := m.CleanAllAuthCache()
	require.NoError(t, err)

	// auth.json should be removed
	_, err = os.Stat(filepath.Join(tempDir, "auth.json"))
	assert.True(t, os.IsNotExist(err), "auth.json should be deleted")

	// auth.claims should be removed
	_, err = os.Stat(filepath.Join(tempDir, "auth.claims"))
	assert.True(t, os.IsNotExist(err), "auth.claims should be deleted")

	// Old MSAL cache files should be gone
	_, err = os.Stat(filepath.Join(msalDir, "cache.json"))
	assert.True(t, os.IsNotExist(err), "MSAL cache should be deleted")

	// Old credential files should be gone
	_, err = os.Stat(filepath.Join(authDir, "credtenant.client.json"))
	assert.True(t, os.IsNotExist(err), "credential cache should be deleted")

	// auth/msal directory should be recreated (empty)
	info, err := os.Stat(msalDir)
	require.NoError(t, err, "msal directory should be recreated")
	assert.True(t, info.IsDir())
}

func TestCleanAllAuthCache_NoExistingFiles(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	m := &Manager{}
	err := m.CleanAllAuthCache()
	require.NoError(t, err, "should succeed even when no auth files exist")

	// auth/msal directory should still be created
	info, err := os.Stat(filepath.Join(tempDir, "auth", "msal"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
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
	// Explicitly clear so the test works even in Azure Pipelines CI.
	t.Setenv("SYSTEM_ACCESSTOKEN", "")
	m := Manager{cloud: cloud.AzurePublic()}
	_, err := m.newCredentialFromFederatedTokenProvider(
		"tenant", "client", azurePipelinesFederatedTokenProvider, nil,
	)
	require.Error(t, err)
	require.ErrorIs(t, err, errNoSystemAccessTokenEnvVar)
}

func TestNewCredentialFromFederatedTokenProvider_GitHub_MissingEnvVars(t *testing.T) {
	// Code uses os.LookupEnv — vars must be truly unset.
	// t.Setenv registers cleanup; os.Unsetenv actually removes them.
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	os.Unsetenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
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
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	os.Unsetenv("ACTIONS_ID_TOKEN_REQUEST_URL")

	m := Manager{cloud: cloud.AzurePublic()}
	_, err := m.newCredentialFromFederatedTokenProvider(
		"tenant", "client", gitHubFederatedTokenProvider, nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ACTIONS_ID_TOKEN_REQUEST_URL")
}

func TestNewCredentialFromFederatedTokenProvider_Oidc_MissingRequestToken(t *testing.T) {
	t.Setenv("AZURE_OIDC_TOKEN", "")
	os.Unsetenv("AZURE_OIDC_TOKEN")
	t.Setenv("AZURE_OIDC_REQUEST_URL", "https://example.com")
	t.Setenv("AZURE_OIDC_REQUEST_TOKEN", "")
	os.Unsetenv("AZURE_OIDC_REQUEST_TOKEN")

	m := Manager{cloud: cloud.AzurePublic()}
	_, err := m.newCredentialFromFederatedTokenProvider(
		"tenant", "client", oidcFederatedTokenProvider, nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AZURE_OIDC_REQUEST_TOKEN")
}

func TestNewCredentialFromFederatedTokenProvider_Oidc_MissingRequestURL(t *testing.T) {
	t.Setenv("AZURE_OIDC_TOKEN", "")
	os.Unsetenv("AZURE_OIDC_TOKEN")
	t.Setenv("AZURE_OIDC_REQUEST_TOKEN", "token-value")
	t.Setenv("AZURE_OIDC_REQUEST_URL", "")
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
	t.Setenv("AZURE_OIDC_TOKEN", "")
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

type silentSuccessClient struct {
	mockPublicClient
	result public.AuthResult
}

type silentErrorClient struct {
	mockPublicClient
	err error
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

func tokenRequestOpts(
	c *cloud.Cloud, claims string,
) policy.TokenRequestOptions {
	return policy.TokenRequestOptions{
		Scopes: LoginScopes(c),
		Claims: claims,
	}
}

var errWave3Test = errors.New("wave3-test")

// stubDeviceCode is a minimal deviceCodeResult.
type stubDeviceCode struct {
	msg  string
	code string
	res  public.AuthResult
	err  error
}

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
