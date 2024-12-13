// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcloud "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/internal/runcontext"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/github"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/oneauth"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/cli/browser"
)

// TODO(azure/azure-dev#710): Right now, we re-use the App Id of the `az` CLI, until we have our own.
//
// nolint:lll
// https://github.com/Azure/azure-cli/blob/azure-cli-2.41.0/src/azure-cli-core/azure/cli/core/auth/identity.py#L23
const azdClientID = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"

// currentUserKey is the key we use in config for the storing identity information of the currently logged in user.
const currentUserKey = "auth.account.currentUser"

// useAzCliAuthKey is the key we use in config to denote that we want to use the az CLI for authentication instead of
// managing it ourselves. The value should be a string as specified by [strconv.ParseBool].
const useAzCliAuthKey = "auth.useAzCliAuth"

// authConfigFileName is the name of the file we store in the user configuration directory which is used to persist
// auth related configuration information (e.g. the home account id of the current user). This information is not secret.
const authConfigFileName = "auth.json"

// azurePipelinesSystemAccessTokenEnvVarName is the name of the environment variable that contains the system access token
// used to auth against the ODIC endpoint for Azure Pipelines. It needs to be set by the task that runs the azd command by
// adding `SYSTEM_ACCESSTOKEN: $(System.AccessToken)` to the `env` section of the task configuration.
const azurePipelinesSystemAccessTokenEnvVarName = "SYSTEM_ACCESSTOKEN"

// errNoSystemAccessTokenEnvVar is returned when the System.AccessToken environment variable is not set.
var errNoSystemAccessTokenEnvVar = fmt.Errorf(
	"system access token not found, ensure the System.AccessToken value is mapped to an environment variable named %s",
	azurePipelinesSystemAccessTokenEnvVarName)

// HttpClient interface as required by MSAL library.
type HttpClient interface {
	// Do sends an HTTP request and returns an HTTP response.
	Do(*http.Request) (*http.Response, error)

	// CloseIdleConnections closes any idle connections in a "keep-alive" state.
	CloseIdleConnections()
}

// Manager manages the authentication system of azd. It allows a user to log in, either as a user principal or service
// principal. Manager stores information so that the user can stay logged in across invocations of the CLI. When logged in
// as a user (either interactively or via a device code flow), we provide a durable cache to MSAL which is used to cache
// information to allow silent logins across process runs. This cache is stored inside the user's home directory, ACL'd such
// that it can only be read by the current user.  In addition, on Windows, this cache is encrypted, using CryptProtectData.
// The home account id of the signed in user is stored as a property under [cCurrentUserKey]. This behavior matches the
// AZ CLI.
//
// When logged in as a service principal, the same cache strategy that backed the MSAL cache is used to store the private
// key or secret and the public components (the client ID and tenant ID) are stored under  [cCurrentUserKey].
//
// Logging out removes this cached authentication data.
//
// You can configure azd to ignore its native credential system and instead delegate to AZ CLI (useful for cases where azd
// does not yet support your preferred method of authentication by setting [cUseLegacyAzCliAuthKey] in config to true.
type Manager struct {
	publicClient        publicClient
	publicClientOptions []public.Option
	cloud               *cloud.Cloud
	configManager       config.FileConfigManager
	userConfigManager   config.UserConfigManager
	credentialCache     Cache
	ghClient            *github.FederatedTokenClient
	httpClient          HttpClient
	console             input.Console
	externalAuthCfg     ExternalAuthConfiguration
}

type ExternalAuthConfiguration struct {
	Endpoint    string
	Key         string
	Transporter policy.Transporter
}

func NewManager(
	configManager config.FileConfigManager,
	userConfigManager config.UserConfigManager,
	cloud *cloud.Cloud,
	httpClient HttpClient,
	console input.Console,
	externalAuthCfg ExternalAuthConfiguration,
) (*Manager, error) {
	cfgRoot, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("getting config dir: %w", err)
	}

	authRoot := filepath.Join(cfgRoot, "auth")
	if err := os.MkdirAll(authRoot, osutil.PermissionDirectoryOwnerOnly); err != nil {
		return nil, fmt.Errorf("creating auth root: %w", err)
	}

	cacheRoot := filepath.Join(authRoot, "msal")
	if err := os.MkdirAll(cacheRoot, osutil.PermissionDirectoryOwnerOnly); err != nil {
		return nil, fmt.Errorf("creating msal cache root: %w", err)
	}

	authorityUrl, err := url.JoinPath(cloud.Configuration.ActiveDirectoryAuthorityHost, "organizations")
	if err != nil {
		return nil, fmt.Errorf("joining authority url: %w", err)
	}

	options := []public.Option{
		public.WithCache(newCache(cacheRoot)),
		public.WithAuthority(authorityUrl),
		public.WithHTTPClient(httpClient),
	}

	publicClientApp, err := public.New(azdClientID, options...)
	if err != nil {
		return nil, fmt.Errorf("creating msal client: %w", err)
	}

	ghClient := github.NewFederatedTokenClient(nil)

	return &Manager{
		publicClient:        &msalPublicClientAdapter{client: &publicClientApp},
		publicClientOptions: options,
		cloud:               cloud,
		configManager:       configManager,
		userConfigManager:   userConfigManager,
		credentialCache:     newCredentialCache(authRoot),
		ghClient:            ghClient,
		httpClient:          httpClient,
		console:             console,
		externalAuthCfg:     externalAuthCfg,
	}, nil
}

// LoginScopes returns the scopes that we request an access token for when checking if a user is signed in.
func LoginScopes(cloud *cloud.Cloud) []string {
	resourceManagerUrl := cloud.Configuration.Services[azcloud.ResourceManager].Endpoint
	return []string{
		fmt.Sprintf("%s//.default", resourceManagerUrl),
	}
}

func (m *Manager) LoginScopes() []string {
	return LoginScopes(m.cloud)
}

func loginScopesMap(cloud *cloud.Cloud) map[string]struct{} {
	resourceManagerUrl := cloud.Configuration.Services[azcloud.ResourceManager].Endpoint

	return map[string]struct{}{resourceManagerUrl: {}}
}

// EnsureLoggedInCredential uses the credential's GetToken method to ensure an access token can be fetched.
// On success, the token we fetched is returned.
func EnsureLoggedInCredential(
	ctx context.Context,
	credential azcore.TokenCredential,
	cloud *cloud.Cloud,
) (*azcore.AccessToken, error) {
	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: LoginScopes(cloud),
	})
	if err != nil {
		return &azcore.AccessToken{}, err
	}

	return &token, nil
}

// CredentialForCurrentUser returns a TokenCredential instance for the current user. If `auth.useLegacyAzCliAuth` is set to
// a truthy value in config, an instance of azidentity.AzureCLICredential is returned instead. To accept the default options,
// pass nil.
func (m *Manager) CredentialForCurrentUser(
	ctx context.Context,
	options *CredentialForCurrentUserOptions,
) (azcore.TokenCredential, error) {

	if options == nil {
		options = &CredentialForCurrentUserOptions{}
	}

	if m.UseExternalAuth() {
		log.Printf("delegating auth to external process")
		return newRemoteCredential(
			m.externalAuthCfg.Endpoint,
			m.externalAuthCfg.Key,
			options.TenantID,
			m.externalAuthCfg.Transporter), nil
	}

	userConfig, err := m.userConfigManager.Load()
	if err != nil {
		return nil, fmt.Errorf("fetching current user: %w", err)
	}

	if shouldUseLegacyAuth(userConfig) {
		log.Printf("delegating auth to az since %s is set to true", useAzCliAuthKey)
		cred, err := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{
			TenantID: options.TenantID,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create credential: %w: %w", err, ErrNoCurrentUser)
		}
		return cred, nil
	}

	authConfig, err := m.readAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("reading auth config: %w", err)
	}

	currentUser, err := readUserProperties(authConfig)
	if errors.Is(err, ErrNoCurrentUser) {
		// User is not logged in, not using az credentials, try CloudShell if possible
		if runcontext.IsRunningInCloudShell() {
			cloudShellCredential, err := m.newCredentialFromCloudShell()
			if err != nil {
				return nil, err
			}
			return cloudShellCredential, nil
		}
		if oneauth.Supported && strings.EqualFold(os.Getenv("IsDevBox"), "True") {
			// Try logging in the active OS account. If that fails for any reason, tell the user to run `azd auth login`.
			if err := m.LoginWithBrokerAccount(); err == nil {
				if config, err := m.readAuthConfig(); err == nil {
					user, err := readUserProperties(config)
					if err == nil && user != nil && user.HomeAccountID != nil && *user.HomeAccountID != "" {
						tenant := options.TenantID
						if tenant == "" {
							tenant = "organizations"
						}
						authority := m.cloud.Configuration.ActiveDirectoryAuthorityHost + tenant
						return oneauth.NewCredential(authority, azdClientID, oneauth.CredentialOptions{
							HomeAccountID: *user.HomeAccountID,
						})
					}
				}
			}
		}
		return nil, ErrNoCurrentUser
	}

	if currentUser.HomeAccountID != nil {
		if currentUser.FromOneAuth {
			tenant := options.TenantID
			if tenant == "" {
				tenant = "organizations"
			}

			authority, err := url.JoinPath(m.cloud.Configuration.ActiveDirectoryAuthorityHost, tenant)
			if err != nil {
				return nil, fmt.Errorf("joining authority url: %w", err)
			}

			return oneauth.NewCredential(authority, azdClientID, oneauth.CredentialOptions{
				HomeAccountID: *currentUser.HomeAccountID,
				NoPrompt:      options.NoPrompt,
			})
		}

		accounts, err := m.publicClient.Accounts(ctx)
		if err != nil {
			return nil, err
		}
		for i, account := range accounts {
			if account.HomeAccountID == *currentUser.HomeAccountID {
				if options.TenantID == "" {
					return newAzdCredential(m.publicClient, &accounts[i], m.cloud), nil
				} else {
					newAuthority := m.cloud.Configuration.ActiveDirectoryAuthorityHost + options.TenantID

					newOptions := make([]public.Option, 0, len(m.publicClientOptions)+1)
					newOptions = append(newOptions, m.publicClientOptions...)

					// It is important that this option comes after the saved public client options since it will
					// override the default authority.
					newOptions = append(newOptions, public.WithAuthority(newAuthority))

					clientWithNewTenant, err := public.New(azdClientID, newOptions...)
					if err != nil {
						return nil, err
					}

					return newAzdCredential(
						&msalPublicClientAdapter{client: &clientWithNewTenant}, &accounts[i], m.cloud), nil
				}
			}
		}
	} else if currentUser.ManagedIdentity {
		clientID := ""
		if currentUser.ClientID != nil {
			clientID = *currentUser.ClientID
		}
		return m.newCredentialFromManagedIdentity(clientID)
	} else if currentUser.TenantID != nil && currentUser.ClientID != nil {
		// by default we used the stored tenant (i.e. the one provided with the tenant id parameter when a user ran
		// `azd auth login`), but we allow an override using the options bag, when
		// TenantID is non-empty and PreferFallbackTenant is not true.
		tenantID := *currentUser.TenantID

		if options.TenantID != "" {
			tenantID = options.TenantID
		}

		ps, err := m.loadSecret(*currentUser.TenantID, *currentUser.ClientID)
		if err != nil {
			return nil, fmt.Errorf("loading secret: %w: %w", err, ErrNoCurrentUser)
		}

		if ps.ClientSecret != nil {
			return m.newCredentialFromClientSecret(tenantID, *currentUser.ClientID, *ps.ClientSecret)
		} else if ps.ClientCertificate != nil {
			return m.newCredentialFromClientCertificate(tenantID, *currentUser.ClientID, *ps.ClientCertificate)
		} else if ps.FederatedAuth != nil && ps.FederatedAuth.TokenProvider != nil {
			return m.newCredentialFromFederatedTokenProvider(
				tenantID, *currentUser.ClientID, *ps.FederatedAuth.TokenProvider, ps.FederatedAuth.ServiceConnectionID)
		}
	}

	return nil, ErrNoCurrentUser
}

type ClaimsForCurrentUserOptions = CredentialForCurrentUserOptions

// ClaimsForCurrentUser returns claims for the currently logged in user.
func (m *Manager) ClaimsForCurrentUser(ctx context.Context, options *ClaimsForCurrentUserOptions) (TokenClaims, error) {
	if options == nil {
		options = &ClaimsForCurrentUserOptions{}
	}
	// The user's credential is used to obtain an access token.
	// This implementation works well even in cases where a remote credential protocol is used to provide the credential.
	cred, err := m.CredentialForCurrentUser(ctx, options)
	if err != nil {
		return TokenClaims{}, err
	}

	accessToken, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes:   LoginScopes(m.cloud),
		TenantID: options.TenantID,
	})
	if err != nil {
		return TokenClaims{}, err
	}

	claims, err := GetClaimsFromAccessToken(accessToken.Token)
	if err != nil {
		return TokenClaims{}, err
	}

	return claims, nil
}

func shouldUseLegacyAuth(cfg config.Config) bool {
	if useLegacyAuth, has := cfg.Get(useAzCliAuthKey); has {
		if use, err := strconv.ParseBool(useLegacyAuth.(string)); err == nil && use {
			return true
		}
	}

	return false
}

// GetLoggedInServicePrincipalTenantID returns the stored service principal's tenant ID.
//
// Service principals are fixed to a particular tenant.
//
// This can be used to determine if the tenant is fixed, and if so short circuit performance intensive tenant-switching
// for service principals.
func (m *Manager) GetLoggedInServicePrincipalTenantID(ctx context.Context) (*string, error) {

	if m.UseExternalAuth() {
		// When delegating to an external system, we have no way to determine what principal was used
		return nil, nil
	}

	cfg, err := m.userConfigManager.Load()
	if err != nil {
		return nil, fmt.Errorf("fetching current user: %w", err)
	}

	if shouldUseLegacyAuth(cfg) {
		// When delegating to az, we have no way to determine what principal was used
		return nil, nil
	}

	authCfg, err := m.readAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("fetching auth config: %w", err)
	}

	currentUser, err := readUserProperties(authCfg)
	if err != nil {
		// No user is logged in, if running in CloudShell use tenant id from
		// CloudShell session (single tenant)
		if runcontext.IsRunningInCloudShell() {
			// Tenant ID is not required when requesting a token from CloudShell
			credential, err := m.CredentialForCurrentUser(ctx, nil)
			if err != nil {
				return nil, err
			}

			token, err := EnsureLoggedInCredential(ctx, credential, m.cloud)
			if err != nil {
				return nil, err
			}

			tenantId, err := GetTenantIdFromToken(token.Token)
			if err != nil {
				return nil, err
			}

			return &tenantId, nil
		}

		return nil, ErrNoCurrentUser
	}

	// Record type of account found
	if currentUser.TenantID != nil {
		tracing.SetGlobalAttributes(fields.AccountTypeKey.String(fields.AccountTypeServicePrincipal))
	}

	if currentUser.HomeAccountID != nil {
		tracing.SetGlobalAttributes(fields.AccountTypeKey.String(fields.AccountTypeUser))
	}

	return currentUser.TenantID, nil
}

func (m *Manager) newCredentialFromManagedIdentity(clientID string) (azcore.TokenCredential, error) {
	options := &azidentity.ManagedIdentityCredentialOptions{}
	if clientID != "" {
		options.ID = azidentity.ClientID(clientID)
	}

	cred, err := azidentity.NewManagedIdentityCredential(options)
	if err != nil {
		return nil, fmt.Errorf("creating credential: %w", err)
	}

	return cred, nil
}

func (m *Manager) newCredentialFromClientSecret(
	tenantID string,
	clientID string,
	clientSecret string,
) (azcore.TokenCredential, error) {
	options := &azidentity.ClientSecretCredentialOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: m.httpClient,
			// TODO: Inject client options instead? this can be done if we're OK
			// using the default user agent string.
			Cloud: m.cloud.Configuration,
		},
	}
	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, options)
	if err != nil {
		return nil, fmt.Errorf("creating credential: %w: %w", err, ErrNoCurrentUser)
	}

	return cred, nil
}

func (m *Manager) newCredentialFromClientCertificate(
	tenantID string,
	clientID string,
	clientCertificate string,
) (azcore.TokenCredential, error) {
	certData, err := base64.StdEncoding.DecodeString(clientCertificate)
	if err != nil {
		return nil, fmt.Errorf("decoding certificate: %w: %w", err, ErrNoCurrentUser)
	}

	certs, key, err := azidentity.ParseCertificates(certData, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing certificate: %w: %w", err, ErrNoCurrentUser)
	}

	options := &azidentity.ClientCertificateCredentialOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: m.httpClient,
			// TODO: Inject client options instead? this can be done if we're OK
			// using the default user agent string.
			Cloud: m.cloud.Configuration,
		},
	}
	cred, err := azidentity.NewClientCertificateCredential(
		tenantID, clientID, certs, key, options)

	if err != nil {
		return nil, fmt.Errorf("creating credential: %w: %w", err, ErrNoCurrentUser)
	}

	return cred, nil
}

func (m *Manager) newCredentialFromFederatedTokenProvider(
	tenantID string,
	clientID string,
	provider federatedTokenProvider,
	serviceConnectionID *string,
) (azcore.TokenCredential, error) {
	clientOptions := azcore.ClientOptions{
		Transport: m.httpClient,
		// TODO: Inject client options instead? this can be done if we're OK
		// using the default user agent string.
		Cloud: m.cloud.Configuration,
	}

	switch provider {
	case gitHubFederatedTokenProvider:
		cred, err := azidentity.NewClientAssertionCredential(
			tenantID,
			clientID,
			func(ctx context.Context) (string, error) {
				federatedToken, err := m.ghClient.TokenForAudience(ctx, "api://AzureADTokenExchange")
				if err != nil {
					return "", fmt.Errorf("fetching federated token: %w", err)
				}

				return federatedToken, nil
			},
			&azidentity.ClientAssertionCredentialOptions{
				ClientOptions: clientOptions,
			})
		if err != nil {
			return nil, fmt.Errorf("creating credential: %w", err)
		}

		return cred, nil

	case azurePipelinesFederatedTokenProvider:
		systemAccessToken := os.Getenv(azurePipelinesSystemAccessTokenEnvVarName)
		if systemAccessToken == "" {
			return nil, errNoSystemAccessTokenEnvVar
		}

		// Guard against the case where the service connection ID is not set because someone manually edited the json
		// files managed by `azd auth login`.
		if serviceConnectionID == nil {
			return nil, errors.New("service connection ID not found, please run `azd auth login` to authenticate")
		}

		cred, err := azidentity.NewAzurePipelinesCredential(
			tenantID, clientID, *serviceConnectionID, systemAccessToken, &azidentity.AzurePipelinesCredentialOptions{
				ClientOptions: clientOptions,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("creating credential: %w: %w", err, ErrNoCurrentUser)
		}

		return cred, nil
	default:
		return nil, fmt.Errorf("unsupported federated token provider: '%s'", string(provider))
	}
}

func (m *Manager) newCredentialFromCloudShell() (azcore.TokenCredential, error) {
	return NewCloudShellCredential(m.httpClient), nil
}

// WithOpenUrl defines a custom strategy for browsing to the url.
type WithOpenUrl func(url string) error

// LoginInteractiveOptions holds the optional inputs for interactive login.
type LoginInteractiveOptions struct {
	TenantID     string
	RedirectPort int
	WithOpenUrl  WithOpenUrl
}

// LoginInteractive opens a browser for authenticate the user.
func (m *Manager) LoginInteractive(
	ctx context.Context,
	scopes []string,
	options *LoginInteractiveOptions) (azcore.TokenCredential, error) {
	if scopes == nil {
		scopes = m.LoginScopes()
	}
	acquireTokenOptions := []public.AcquireInteractiveOption{}
	if options == nil {
		options = &LoginInteractiveOptions{}
	}

	if options.RedirectPort > 0 {
		acquireTokenOptions = append(
			acquireTokenOptions, public.WithRedirectURI(fmt.Sprintf("http://localhost:%d", options.RedirectPort)))
	}

	if options.TenantID != "" {
		acquireTokenOptions = append(acquireTokenOptions, public.WithTenantID(options.TenantID))
	}

	if options.WithOpenUrl != nil {
		acquireTokenOptions = append(acquireTokenOptions, public.WithOpenURL(options.WithOpenUrl))
	}

	res, err := m.publicClient.AcquireTokenInteractive(ctx, scopes, acquireTokenOptions...)
	if err != nil {
		return nil, err
	}

	if err := m.saveLoginForPublicClient(res); err != nil {
		return nil, err
	}

	return newAzdCredential(m.publicClient, &res.Account, m.cloud), nil
}

// LoginWithBrokerAccount logs in an account provided by the system authentication broker via OneAuth.
// For example, it will log in the user currently signed in to Windows. This method never prompts for
// user interaction and returns an error when the broker doesn't provide an account.
func (m *Manager) LoginWithBrokerAccount() error {
	accountID, err := oneauth.LogInSilently(azdClientID)
	if err == nil {
		err = m.saveUserProperties(&userProperties{
			FromOneAuth:   true,
			HomeAccountID: &accountID,
		})
	}
	return err
}

// LoginWithOneAuth starts OneAuth's interactive login flow.
func (m *Manager) LoginWithOneAuth(ctx context.Context, tenantID string, scopes []string) error {
	if len(scopes) == 0 {
		scopes = m.LoginScopes()
	}
	authority := m.cloud.Configuration.ActiveDirectoryAuthorityHost + tenantID
	accountID, err := oneauth.LogIn(authority, azdClientID, strings.Join(scopes, " "))
	if err == nil {
		err = m.saveUserProperties(&userProperties{
			FromOneAuth:   true,
			HomeAccountID: &accountID,
		})
	}
	return err
}

func (m *Manager) LoginWithDeviceCode(
	ctx context.Context, tenantID string, scopes []string, withOpenUrl WithOpenUrl) (azcore.TokenCredential, error) {
	if scopes == nil {
		scopes = m.LoginScopes()
	}
	options := []public.AcquireByDeviceCodeOption{}
	if tenantID != "" {
		options = append(options, public.WithTenantID(tenantID))
	}

	if withOpenUrl == nil {
		withOpenUrl = browser.OpenURL
	}

	code, err := m.publicClient.AcquireTokenByDeviceCode(ctx, scopes, options...)
	if err != nil {
		return nil, err
	}

	url := "https://microsoft.com/devicelogin"

	if runcontext.IsRunningInCloudShell() {
		m.console.MessageUxItem(ctx, &ux.MultilineMessage{
			Lines: []string{
				// nolint:lll
				"Cloud Shell is automatically authenticated under the initial account used to sign in. Run 'azd auth login' only if you need to use a different account.",
				fmt.Sprintf(
					"To sign in, use a web browser to open the page %s and enter the code %s to authenticate.",
					output.WithUnderline("%s", url),
					output.WithBold("%s", code.UserCode()),
				),
			},
		})
	} else {
		m.console.Message(ctx, fmt.Sprintf("Start by copying the next code: %s", output.WithBold("%s", code.UserCode())))

		if err := withOpenUrl(url); err != nil {
			log.Println("error launching browser: ", err.Error())
			m.console.Message(ctx, fmt.Sprintf("Error launching browser. Manually go to: %s", url))
		}
		m.console.Message(ctx, "Waiting for you to complete authentication in the browser...")
	}

	res, err := code.AuthenticationResult(ctx)
	if err != nil {
		return nil, err
	}
	m.console.Message(ctx, "Device code authentication completed.")

	if err := m.saveLoginForPublicClient(res); err != nil {
		return nil, err
	}

	return newAzdCredential(m.publicClient, &res.Account, m.cloud), nil

}

func (m *Manager) LoginWithManagedIdentity(ctx context.Context, clientID string) (azcore.TokenCredential, error) {
	options := &azidentity.ManagedIdentityCredentialOptions{}
	if clientID != "" {
		options.ID = azidentity.ClientID(clientID)
	}

	cred, err := azidentity.NewManagedIdentityCredential(options)
	if err != nil {
		return nil, fmt.Errorf("creating credential: %w", err)
	}

	if err := m.saveLoginForManagedIdentity(clientID); err != nil {
		return nil, err
	}

	return cred, nil
}

func (m *Manager) LoginWithServicePrincipalSecret(
	ctx context.Context, tenantId, clientId, clientSecret string,
) (azcore.TokenCredential, error) {
	cred, err := azidentity.NewClientSecretCredential(tenantId, clientId, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("creating credential: %w", err)
	}

	if err := m.saveLoginForServicePrincipal(
		tenantId,
		clientId,
		&persistedSecret{
			ClientSecret: &clientSecret,
		},
	); err != nil {
		return nil, err
	}

	return cred, nil
}

func (m *Manager) LoginWithServicePrincipalCertificate(
	ctx context.Context, tenantId, clientId string, certData []byte,
) (azcore.TokenCredential, error) {
	certs, key, err := azidentity.ParseCertificates(certData, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing certificate: %w", err)
	}

	cred, err := azidentity.NewClientCertificateCredential(tenantId, clientId, certs, key, nil)
	if err != nil {
		return nil, fmt.Errorf("creating credential: %w", err)
	}

	encodedCert := base64.StdEncoding.EncodeToString(certData)

	if err := m.saveLoginForServicePrincipal(
		tenantId,
		clientId,
		&persistedSecret{
			ClientCertificate: &encodedCert,
		},
	); err != nil {
		return nil, err
	}

	return cred, nil
}

func (m *Manager) LoginWithGitHubFederatedTokenProvider(
	ctx context.Context, tenantId, clientId string,
) (azcore.TokenCredential, error) {
	cred, err := m.newCredentialFromFederatedTokenProvider(tenantId, clientId, gitHubFederatedTokenProvider, nil)
	if err != nil {
		return nil, err
	}

	if err := m.saveLoginForServicePrincipal(
		tenantId,
		clientId,
		&persistedSecret{
			FederatedAuth: &federatedAuth{
				TokenProvider: &gitHubFederatedTokenProvider,
			},
		},
	); err != nil {
		return nil, err
	}

	return cred, nil
}

func (m *Manager) LoginWithAzurePipelinesFederatedTokenProvider(
	ctx context.Context, tenantID string, clientID string, serviceConnectionID string,
) (azcore.TokenCredential, error) {
	systemAccessToken := os.Getenv(azurePipelinesSystemAccessTokenEnvVarName)

	if systemAccessToken == "" {
		return nil, errNoSystemAccessTokenEnvVar
	}

	options := &azidentity.AzurePipelinesCredentialOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: m.httpClient,
			// TODO: Inject client options instead? this can be done if we're OK
			// using the default user agent string.
			Cloud: m.cloud.Configuration,
		},
	}

	cred, err := azidentity.NewAzurePipelinesCredential(tenantID, clientID, serviceConnectionID, systemAccessToken, options)
	if err != nil {
		return nil, fmt.Errorf("creating credential: %w", err)
	}

	if err := m.saveLoginForServicePrincipal(tenantID, clientID, &persistedSecret{
		FederatedAuth: &federatedAuth{
			TokenProvider:       &azurePipelinesFederatedTokenProvider,
			ServiceConnectionID: &serviceConnectionID,
		},
	}); err != nil {
		return nil, err
	}

	return cred, nil
}

// Logout signs out the current user and removes any cached authentication information
func (m *Manager) Logout(ctx context.Context) error {
	act, err := m.getSignedInAccount(ctx)
	if err != nil && !errors.Is(err, ErrNoCurrentUser) {
		return fmt.Errorf("fetching current user: %w", err)
	}

	if act != nil {
		if err := m.publicClient.RemoveAccount(ctx, *act); err != nil {
			return fmt.Errorf("removing account from msal cache: %w", err)
		}
	}

	cfg, err := m.readAuthConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// we are fine to ignore the error here, it just means there's nothing to clean up.
	currentUser, _ := readUserProperties(cfg)
	if currentUser != nil {
		if currentUser.FromOneAuth {
			if err := oneauth.Logout(azdClientID); err != nil {
				return fmt.Errorf("logging out of OneAuth: %w", err)
			}
		} else if currentUser.TenantID != nil && currentUser.ClientID != nil {
			// When logged in as a service principal, remove the stored credential
			if err := m.saveLoginForServicePrincipal(
				*currentUser.TenantID, *currentUser.ClientID, &persistedSecret{},
			); err != nil {
				return fmt.Errorf("removing authentication secrets: %w", err)
			}
		}
	}

	if err := cfg.Unset(currentUserKey); err != nil {
		return fmt.Errorf("un-setting current user: %w", err)
	}

	if err := m.saveAuthConfig(cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	return nil
}

func (m *Manager) UseExternalAuth() bool {
	return m.externalAuthCfg.Endpoint != "" && m.externalAuthCfg.Key != ""
}

func (m *Manager) saveLoginForPublicClient(res public.AuthResult) error {
	if err := m.saveUserProperties(&userProperties{HomeAccountID: &res.Account.HomeAccountID}); err != nil {
		return err
	}

	return nil
}

func (m *Manager) saveLoginForManagedIdentity(clientID string) error {
	props := &userProperties{ManagedIdentity: true}
	if clientID != "" {
		props.ClientID = &clientID
	}
	if err := m.saveUserProperties(props); err != nil {
		return err
	}

	return nil
}

func (m *Manager) saveLoginForServicePrincipal(tenantId, clientId string, secret *persistedSecret) error {
	if err := m.saveSecret(tenantId, clientId, secret); err != nil {
		return err
	}

	if err := m.saveUserProperties(&userProperties{ClientID: &clientId, TenantID: &tenantId}); err != nil {
		return err
	}

	return nil
}

// getSignedInAccount fetches the public.Account for the signed in user, or nil if one does not exist
// (e.g when logged in with a service principal).
func (m *Manager) getSignedInAccount(ctx context.Context) (*public.Account, error) {
	cfg, err := m.readAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("fetching current user: %w", err)
	}

	currentUser, err := readUserProperties(cfg)
	if err != nil {
		return nil, ErrNoCurrentUser
	}

	if currentUser.HomeAccountID != nil {
		accounts, err := m.publicClient.Accounts(ctx)
		if err != nil {
			return nil, err
		}
		for _, account := range accounts {
			if account.HomeAccountID == *currentUser.HomeAccountID {
				return &account, nil
			}
		}
	}

	return nil, nil
}

// saveUserProperties writes the properties under [cCurrentUserKey], overwriting any existing value.
func (m *Manager) saveUserProperties(user *userProperties) error {
	cfg, err := m.readAuthConfig()
	if err != nil {
		return fmt.Errorf("fetching current user: %w", err)
	}

	if err := cfg.Set(currentUserKey, *user); err != nil {
		return fmt.Errorf("setting account id in config: %w", err)
	}

	return m.saveAuthConfig(cfg)
}

// readAuthConfig loads the configuration from [cAuthConfigFileName] and returns a parsed version of it. If the config
// file does not exist, an empty [config.Config] is returned, with no error.
func (m *Manager) readAuthConfig() (config.Config, error) {
	cfgPath, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("getting user config dir: %w", err)
	}

	authCfgFile := filepath.Join(cfgPath, authConfigFileName)

	authCfg, err := m.configManager.Load(authCfgFile)
	if err == nil {
		return authCfg, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading auth config: %w", err)
	}

	// We used to store auth related configuration in the user configuration file directly. If above file did not exist,
	// see if there is the data in the old location, and if so migrate it to the new location. This upgrades the old
	// format to the new format.
	userCfg, err := m.userConfigManager.Load()
	if err != nil {
		return nil, fmt.Errorf("reading user config: %w", err)
	}

	curUserData, has := userCfg.Get(currentUserKey)
	if !has {
		return config.NewEmptyConfig(), nil
	}

	authCfg = config.NewEmptyConfig()
	if err := authCfg.Set(currentUserKey, curUserData); err != nil {
		return nil, err
	}

	if err := m.saveAuthConfig(authCfg); err != nil {
		return nil, err
	}

	if err := userCfg.Unset(currentUserKey); err != nil {
		return nil, err
	}

	if err := m.userConfigManager.Save(userCfg); err != nil {
		return nil, err
	}

	return authCfg, nil
}

func (m *Manager) saveAuthConfig(c config.Config) error {
	cfgPath, err := config.GetUserConfigDir()
	if err != nil {
		return fmt.Errorf("getting user config dir: %w", err)
	}

	authCfgFile := filepath.Join(cfgPath, authConfigFileName)

	return m.configManager.Save(c, authCfgFile)
}

// persistedSecretLookupKey returns the cache key we use for a given tenantId, clientId pair.
func persistedSecretLookupKey(tenantId, clientId string) string {
	return fmt.Sprintf("%s.%s", tenantId, clientId)
}

// loadSecret reads a secret from the credential cache for a given client and tenant.
func (m *Manager) loadSecret(tenantId, clientId string) (*persistedSecret, error) {
	val, err := m.credentialCache.Read(persistedSecretLookupKey(tenantId, clientId))
	if err != nil {
		return nil, err
	}

	var ps persistedSecret

	if err := json.Unmarshal(val, &ps); err != nil {
		return nil, err
	}

	return &ps, nil
}

// saveSecret writes a secret into the credential cache for a given client and tenant.
func (m *Manager) saveSecret(tenantId, clientId string, ps *persistedSecret) error {
	data, err := json.Marshal(ps)
	if err != nil {
		return err
	}

	return m.credentialCache.Set(persistedSecretLookupKey(tenantId, clientId), data)
}

type CredentialForCurrentUserOptions struct {
	// NoPrompt controls whether the credential may prompt for user interaction.
	NoPrompt bool
	// The tenant ID to use when constructing the credential, instead of the default tenant.
	TenantID string
}

// persistedSecret is the model type for the value we store in the credential cache. It is logically a discriminated union
// of the different supported authentication modes
type persistedSecret struct {
	// The client secret.
	ClientSecret *string `json:"clientSecret,omitempty"`

	// The bytes of the client certificate, which can be presented to azidentity.ParseCertificates, encoded as a
	// base64 string.
	ClientCertificate *string `json:"clientCertificate,omitempty"`

	// The federated auth credential.
	FederatedAuth *federatedAuth `json:"federatedAuth,omitempty"`
}

// federated auth token providers
var (
	gitHubFederatedTokenProvider         federatedTokenProvider = "github"
	azurePipelinesFederatedTokenProvider federatedTokenProvider = "azure-pipelines"
)

// token provider for federated auth
type federatedTokenProvider string

// federatedAuth stores federated authentication information.
type federatedAuth struct {
	// The auth token provider. Tokens are obtained by calling the provider as needed.
	TokenProvider *federatedTokenProvider `json:"tokenProvider,omitempty"`
	// The ID of the service connection to use for Azure Pipelines federated auth. This is only set when the TokenProvider
	// is "azure-pipelines".
	ServiceConnectionID *string `json:"serviceConnectionId,omitempty"`
}

// userProperties is the model type for the value we store in the user's config. It is logically a discriminated union of
// either an home account id (when logging in using a public client) or a client and tenant id (when using a confidential
// client).
type userProperties struct {
	ManagedIdentity bool    `json:"managedIdentity,omitempty"`
	HomeAccountID   *string `json:"homeAccountId,omitempty"`
	FromOneAuth     bool    `json:"fromOneAuth,omitempty"`
	ClientID        *string `json:"clientId,omitempty"`
	TenantID        *string `json:"tenantId,omitempty"`
}

func readUserProperties(cfg config.Config) (*userProperties, error) {
	currentUser, has := cfg.Get(currentUserKey)
	if !has {
		return nil, ErrNoCurrentUser
	}

	data, err := json.Marshal(currentUser)
	if err != nil {
		return nil, err
	}

	user := userProperties{}
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, err
	}

	return &user, nil
}
