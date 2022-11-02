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
	"github.com/azure/azure-dev/cli/azd/pkg/config"
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
const cUseLegacyAzCliAuthKey = "auth.useLegacyAzCliAuth"

// The scopes to request when acquiring our token during the login flow or when requesting a token to validate if the client
// is logged in.
var cLoginScopes = []string{"https://management.azure.com//.default"}

// authDirectoryFileMode is the file mode used to create the folder that is used for auth folder and sub-folders.
const authDirectoryFileMode = 0700

type Manager struct {
	publicClient    *public.Client
	configManager   config.Manager
	credentialCache exportReplaceWithErrors
}

func NewManager(configManager config.Manager) (*Manager, error) {
	cfgRoot, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("getting config dir: %w", err)
	}

	authRoot := filepath.Join(cfgRoot, "auth")
	if err := os.MkdirAll(authRoot, authDirectoryFileMode); err != nil {
		return nil, fmt.Errorf("creating auth root: %w", err)
	}

	cacheRoot := filepath.Join(authRoot, "msal")
	if err := os.MkdirAll(cacheRoot, authDirectoryFileMode); err != nil {
		return nil, fmt.Errorf("creating msal cache root: %w", err)
	}

	publicClientApp, err := public.New(cAZD_CLIENT_ID, public.WithCache(newCache(cacheRoot)))
	if err != nil {
		return nil, fmt.Errorf("creating msal client: %w", err)
	}

	return &Manager{
		publicClient:    &publicClientApp,
		configManager:   configManager,
		credentialCache: newCredentialCache(authRoot),
	}, nil
}

var ErrNoCurrentUser = errors.New("not logged in, run `azd login` to login")

// EnsureLoggedInCredential uses the credential's GetToken method to ensure an access token can be fetched. If this fails,
// nil, ErrNoCurrentUser is returned. On success, the token we fetched is returned.
func EnsureLoggedInCredential(ctx context.Context, credential azcore.TokenCredential) (azcore.AccessToken, error) {
	tok, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: cLoginScopes,
	})
	if err != nil {
		return azcore.AccessToken{}, ErrNoCurrentUser
	}

	return tok, nil
}

// CredentialForCurrentUser returns a TokenCredential instance for the current user. If `auth.useLegacyAzCliAuth` is set to
// a truthy value in config, an instance of azidentity.AzureCLICredential is returned instead.
func (m *Manager) CredentialForCurrentUser(ctx context.Context) (azcore.TokenCredential, error) {
	cfg, err := config.GetUserConfig(m.configManager)
	if err != nil {
		return nil, fmt.Errorf("fetching current user: %w", err)
	}

	if useLegacyAuth, has := cfg.Get(cUseLegacyAzCliAuthKey); has {
		if use, err := strconv.ParseBool(useLegacyAuth.(string)); err != nil && use {
			cred, err := azidentity.NewAzureCLICredential(nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create credential: %v: %w", err, ErrNoCurrentUser)
			}
			return cred, nil
		}
	}

	currentUser, has := cfg.Get(cCurrentUserKey)
	if !has {
		return nil, ErrNoCurrentUser
	}

	currentUserData := currentUser.(map[string]any)

	if _, has := currentUserData["homeId"]; has {
		currentUserHomeId := currentUserData["homeId"].(string)

		for _, account := range m.publicClient.Accounts() {
			if account.HomeAccountID == currentUserHomeId {
				return newAzdCredential(m.publicClient, &account), nil
			}

			log.Printf("ignoring cached account with home id '%s', does not match '%s'",
				account.HomeAccountID, currentUserHomeId)
		}
	} else if _, has := currentUserData["clientId"]; has {
		clientId := currentUserData["clientId"].(string)
		tenantId := currentUserData["tenantId"].(string)

		ps, err := m.loadSecret(persistedSecretLookupKey(tenantId, clientId))
		if err != nil {
			return nil, fmt.Errorf("loading secret: %v: %w", err, ErrNoCurrentUser)
		}

		cred, err := azidentity.NewClientSecretCredential(tenantId, clientId, *ps.ClientSecret, nil)
		if err != nil {
			return nil, fmt.Errorf("creating credential: %v: %w", err, ErrNoCurrentUser)
		}

		return cred, nil
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
	fmt.Fprintln(deviceCodeWriter, code.Result.Message)

	res, err := code.AuthenticationResult(ctx)
	if err != nil {
		return nil, err
	}

	if err := m.saveLoginForPublicClient(res); err != nil {
		return nil, err
	}

	return newAzdCredential(m.publicClient, &res.Account), nil

}

func (m *Manager) LoginWithServicePrincipal(
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

	cfg, err := config.GetUserConfig(m.configManager)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if cur, has := cfg.Get(cCurrentUserKey); has {
		props := cur.(map[string]any)

		// When logged in as a service principal, remove the stored credential
		if _, ok := props["clientId"]; ok {
			clientId := (props["clientId"]).(string)
			tenantId := (props["tenantId"]).(string)

			if err := m.saveLoginForServicePrincipal(tenantId, clientId, &persistedSecret{}); err != nil {
				return fmt.Errorf("removing authentication secrets: %w", err)
			}
		}
	}

	if err := cfg.Unset(cCurrentUserKey); err != nil {
		return fmt.Errorf("un-setting current user: %w", err)
	}

	if err := config.SaveUserConfig(m.configManager, cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	return nil
}

func (m *Manager) saveLoginForPublicClient(res public.AuthResult) error {
	if err := m.saveCurrentUserProperties(map[string]any{"homeId": res.Account.HomeAccountID}); err != nil {
		return err
	}

	return nil
}

func (m *Manager) saveLoginForServicePrincipal(tenantId, clientId string, secret *persistedSecret) error {
	if err := m.saveSecret(secret, persistedSecretLookupKey(tenantId, clientId)); err != nil {
		return err
	}

	if err := m.saveCurrentUserProperties(map[string]any{"tenantId": tenantId, "clientId": clientId}); err != nil {
		return err
	}

	return nil
}

// getSignedInAccount fetches the public.Account for the signed in user, or nil if one does not exist
// (e.g when logged in with a service principal).
func (m *Manager) getSignedInAccount(ctx context.Context) (*public.Account, error) {
	cfg, err := config.GetUserConfig(m.configManager)
	if err != nil {
		return nil, fmt.Errorf("fetching current user: %w", err)
	}

	currentUser, has := cfg.Get(cCurrentUserKey)
	if !has {
		return nil, ErrNoCurrentUser
	}

	currentUserData := currentUser.(map[string]any)

	if _, has := currentUserData["homeId"]; has {
		currentUserHomeId := currentUserData["homeId"].(string)

		for _, account := range m.publicClient.Accounts() {
			if account.HomeAccountID == currentUserHomeId {
				return &account, nil
			}
		}
	}

	return nil, nil
}

// saveCurrentUserProperties writes the properties under [cCurrentUserKey], overwriting any existing value.
func (m *Manager) saveCurrentUserProperties(properties map[string]any) error {
	cfg, err := config.GetUserConfig(m.configManager)
	if err != nil {
		return fmt.Errorf("fetching current user: %w", err)
	}

	if err := cfg.Set(cCurrentUserKey, properties); err != nil {
		return fmt.Errorf("setting account id in config: %w", err)
	}

	return config.SaveUserConfig(m.configManager, cfg)
}

// persistedSecretLookupKey returns the cache key we use for a given tenantId, clientId pair.
func persistedSecretLookupKey(tenantId, clientId string) string {
	return fmt.Sprintf("%s.%s", tenantId, clientId)
}

// loadSecret reads a secret from the credential cache with a given key.
func (m *Manager) loadSecret(key string) (*persistedSecret, error) {
	var data fixedMarshaller

	if err := m.credentialCache.Replace(&data, key); err != nil {
		return nil, err
	}

	var ps persistedSecret

	if err := json.Unmarshal(data.val, &ps); err != nil {
		return nil, err
	}

	return &ps, nil
}

// saveSecret writes a secret into the credential cache with a given key.
func (m *Manager) saveSecret(ps *persistedSecret, key string) error {
	data, err := json.Marshal(ps)
	if err != nil {
		return err
	}

	return m.credentialCache.Export(&fixedMarshaller{val: data}, key)
}

// persistedSecret is the model type for the value we store in the credential cache. It is logically a discriminated union
// of a client secret and certificate.
type persistedSecret struct {
	// The client secret.
	ClientSecret *string `json:"clientSecret,omitempty"`

	// The bytes of the client certificate, which can be presented to azidentity.ParseCertificates, encoded as a
	// base64 string.
	ClientCertificate *string `json:"clientCertificate,omitempty"`
}
