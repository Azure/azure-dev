// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/github"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// TODO(azure/azure-dev#710): Right now, we re-use the App Id of the `az` CLI, until we have our own.
//
// nolint:lll
// https://github.com/Azure/azure-cli/blob/azure-cli-2.41.0/src/azure-cli-core/azure/cli/core/auth/identity.py#L23
const cAZD_CLIENT_ID = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"

// cCurrentUserKey is the key we use in config for the storing identity information of the currently logged in user.
const cCurrentUserKey = "auth.account.currentUser"

// cUseAzCli is the key we use in config to denote that we want to use the az CLI for authentication instead of managing
// it ourselves. The value should be a string as specified by [strconv.ParseBool].
const cUseAzCliAuthKey = "auth.useAzCliAuth"

// The scopes to request when acquiring our token during the login flow or when requesting a token to validate if the client
// is logged in.
var cLoginScopes = []string{azure.ManagementScope}

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
	publicClient    publicClient
	configManager   config.UserConfigManager
	credentialCache Cache
	ghClient        *github.FederatedTokenClient
}

func NewManager(configManager config.UserConfigManager) (*Manager, error) {
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

	publicClientApp, err := public.New(cAZD_CLIENT_ID, public.WithCache(newCache(cacheRoot)))
	if err != nil {
		return nil, fmt.Errorf("creating msal client: %w", err)
	}

	ghClient := github.NewFederatedTokenClient(nil)

	return &Manager{
		publicClient:    &msalPublicClientAdapter{client: &publicClientApp},
		configManager:   configManager,
		credentialCache: newCredentialCache(authRoot),
		ghClient:        ghClient,
	}, nil
}

var ErrNoCurrentUser = errors.New("not logged in, run `azd login` to login")

// EnsureLoggedInCredential uses the credential's GetToken method to ensure an access token can be fetched. If this fails,
// nil, ErrNoCurrentUser is returned. On success, the token we fetched is returned.
func EnsureLoggedInCredential(ctx context.Context, credential azcore.TokenCredential) (*azcore.AccessToken, error) {
	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: cLoginScopes,
	})
	if err != nil {
		return &azcore.AccessToken{}, ErrNoCurrentUser
	}

	return &token, nil
}

// CredentialForCurrentUser returns a TokenCredential instance for the current user. If `auth.useLegacyAzCliAuth` is set to
// a truthy value in config, an instance of azidentity.AzureCLICredential is returned instead.
func (m *Manager) CredentialForCurrentUser(ctx context.Context) (azcore.TokenCredential, error) {
	cfg, err := m.configManager.Load()
	if err != nil {
		return nil, fmt.Errorf("fetching current user: %w", err)
	}

	if useLegacyAuth, has := cfg.Get(cUseAzCliAuthKey); has {
		if use, err := strconv.ParseBool(useLegacyAuth.(string)); err == nil && use {
			log.Printf("delegating auth to az since %s is set to true", cUseAzCliAuthKey)
			cred, err := azidentity.NewAzureCLICredential(nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create credential: %v: %w", err, ErrNoCurrentUser)
			}
			return cred, nil
		}
	}

	currentUser, err := readUserProperties(cfg)
	if err != nil {
		return nil, ErrNoCurrentUser
	}

	if currentUser.HomeAccountID != nil {
		for _, account := range m.publicClient.Accounts() {
			if account.HomeAccountID == *currentUser.HomeAccountID {
				return newAzdCredential(m.publicClient, &account), nil
			}
		}
	} else if currentUser.TenantID != nil && currentUser.ClientID != nil {
		ps, err := m.loadSecret(*currentUser.TenantID, *currentUser.ClientID)
		if err != nil {
			return nil, fmt.Errorf("loading secret: %v: %w", err, ErrNoCurrentUser)
		}

		if ps.ClientSecret != nil {
			cred, err := azidentity.NewClientSecretCredential(
				*currentUser.TenantID, *currentUser.ClientID, *ps.ClientSecret, nil,
			)

			if err != nil {
				return nil, fmt.Errorf("creating credential: %v: %w", err, ErrNoCurrentUser)
			}

			return cred, nil

		} else if ps.ClientCertificate != nil {
			certData, err := base64.StdEncoding.DecodeString(*ps.ClientCertificate)
			if err != nil {
				return nil, fmt.Errorf("decoding certificate: %v: %w", err, ErrNoCurrentUser)
			}

			certs, key, err := azidentity.ParseCertificates(certData, nil)
			if err != nil {
				return nil, fmt.Errorf("parsing certificate: %v: %w", err, ErrNoCurrentUser)
			}

			cred, err := azidentity.NewClientCertificateCredential(
				*currentUser.TenantID, *currentUser.ClientID, certs, key, nil,
			)

			if err != nil {
				return nil, fmt.Errorf("creating credential: %v: %w", err, ErrNoCurrentUser)
			}

			return cred, nil
		} else if ps.FederatedToken != nil {
			cred, err := azidentity.NewClientAssertionCredential(
				*currentUser.TenantID,
				*currentUser.ClientID,
				func(_ context.Context) (string, error) {
					return *ps.FederatedToken, nil
				},
				nil,
			)

			if err != nil {
				return nil, fmt.Errorf("creating credential: %v: %w", err, ErrNoCurrentUser)
			}

			return cred, nil
		}
	}

	return nil, ErrNoCurrentUser
}

func (m *Manager) LoginInteractive(ctx context.Context) (azcore.TokenCredential, error) {
	res, err := m.publicClient.AcquireTokenInteractive(ctx, cLoginScopes)
	if err != nil {
		return nil, err
	}

	if err := m.saveLoginForPublicClient(res); err != nil {
		return nil, err
	}

	return newAzdCredential(m.publicClient, &res.Account), nil
}

func (m *Manager) LoginWithDeviceCode(ctx context.Context, deviceCodeWriter io.Writer) (azcore.TokenCredential, error) {
	code, err := m.publicClient.AcquireTokenByDeviceCode(ctx, cLoginScopes)
	if err != nil {
		return nil, err
	}

	// Display the message to the end user as to what to do next, then block waiting for them to complete
	// the flow.
	fmt.Fprintln(deviceCodeWriter, code.Message())

	res, err := code.AuthenticationResult(ctx)
	if err != nil {
		return nil, err
	}

	if err := m.saveLoginForPublicClient(res); err != nil {
		return nil, err
	}

	return newAzdCredential(m.publicClient, &res.Account), nil

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

func (m *Manager) LoginWithServicePrincipalFederatedToken(
	ctx context.Context, tenantId, clientId, federatedToken string,
) (azcore.TokenCredential, error) {
	cred, err := azidentity.NewClientAssertionCredential(
		tenantId,
		clientId,
		func(_ context.Context) (string, error) {
			return federatedToken, nil
		},
		nil)
	if err != nil {
		return nil, fmt.Errorf("creating credential: %w", err)
	}

	if err := m.saveLoginForServicePrincipal(
		tenantId,
		clientId,
		&persistedSecret{
			FederatedToken: &federatedToken,
		},
	); err != nil {
		return nil, err
	}

	return cred, nil
}

func (m *Manager) LoginWithServicePrincipalFederatedTokenProvider(
	ctx context.Context, tenantId, clientId, federatedTokenProvider string,
) (azcore.TokenCredential, error) {

	if federatedTokenProvider != "github" {
		return nil, fmt.Errorf("unsupported federated token provider: '%s'", federatedTokenProvider)
	}

	federatedToken, err := m.ghClient.TokenForAudience(ctx, "api://AzureADTokenExchange")
	if err != nil {
		return nil, fmt.Errorf("fetching federated token: %w", err)
	}

	cred, err := azidentity.NewClientAssertionCredential(
		tenantId,
		clientId,
		func(_ context.Context) (string, error) {
			return federatedToken, nil
		},
		nil)
	if err != nil {
		return nil, fmt.Errorf("creating credential: %w", err)
	}

	if err := m.saveLoginForServicePrincipal(
		tenantId,
		clientId,
		&persistedSecret{
			FederatedToken: &federatedToken,
		},
	); err != nil {
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
		if err := m.publicClient.RemoveAccount(*act); err != nil {
			return fmt.Errorf("removing account from msal cache: %w", err)
		}
	}

	cfg, err := m.configManager.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// we are fine to ignore the error here, it just means there's nothing to clean up.
	currentUser, _ := readUserProperties(cfg)

	// When logged in as a service principal, remove the stored credential
	if currentUser != nil && currentUser.TenantID != nil && currentUser.ClientID != nil {
		if err := m.saveLoginForServicePrincipal(
			*currentUser.TenantID, *currentUser.ClientID, &persistedSecret{},
		); err != nil {
			return fmt.Errorf("removing authentication secrets: %w", err)
		}
	}

	if err := cfg.Unset(cCurrentUserKey); err != nil {
		return fmt.Errorf("un-setting current user: %w", err)
	}

	if err := m.configManager.Save(cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	return nil
}

func (m *Manager) saveLoginForPublicClient(res public.AuthResult) error {
	if err := m.saveUserProperties(&userProperties{HomeAccountID: &res.Account.HomeAccountID}); err != nil {
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
	cfg, err := m.configManager.Load()
	if err != nil {
		return nil, fmt.Errorf("fetching current user: %w", err)
	}

	currentUser, err := readUserProperties(cfg)
	if err != nil {
		return nil, ErrNoCurrentUser
	}

	if currentUser.HomeAccountID != nil {
		for _, account := range m.publicClient.Accounts() {
			if account.HomeAccountID == *currentUser.HomeAccountID {
				return &account, nil
			}
		}
	}

	return nil, nil
}

// saveUserProperties writes the properties under [cCurrentUserKey], overwriting any existing value.
func (m *Manager) saveUserProperties(user *userProperties) error {
	cfg, err := m.configManager.Load()
	if err != nil {
		return fmt.Errorf("fetching current user: %w", err)
	}

	if err := cfg.Set(cCurrentUserKey, *user); err != nil {
		return fmt.Errorf("setting account id in config: %w", err)
	}

	return m.configManager.Save(cfg)
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

// persistedSecret is the model type for the value we store in the credential cache. It is logically a discriminated union
// of a client secret and certificate.
type persistedSecret struct {
	// The client secret.
	ClientSecret *string `json:"clientSecret,omitempty"`

	// The bytes of the client certificate, which can be presented to azidentity.ParseCertificates, encoded as a
	// base64 string.
	ClientCertificate *string `json:"clientCertificate,omitempty"`

	// The federated client credential.
	FederatedToken *string `json:"federatedToken,omitempty"`
}

// userProperties is the model type for the value we store in the user's config. It is logically a discriminated union of
// either an home account id (when logging in using a public client) or a client and tenant id (when using a confidential
// client).
type userProperties struct {
	HomeAccountID *string `json:"homeAccountId,omitempty"`
	ClientID      *string `json:"clientId,omitempty"`
	TenantID      *string `json:"tenantId,omitempty"`
}

func readUserProperties(cfg config.Config) (*userProperties, error) {
	currentUser, has := cfg.Get(cCurrentUserKey)
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
