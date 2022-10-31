// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

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

// The scopes to request when acquiring our token during the login flow.
var cLoginScopes = []string{"https://management.azure.com//.default"}

// cacheDirectoryFileMode is the file mode used to create the folder that is used for the MSAL cache.
const cacheDirectoryFileMode = 0700

type Manager struct {
	out           io.Writer
	publicClient  *public.Client
	configManager config.Manager
	cacheRoot     string
}

func NewManager(out io.Writer, configManager config.Manager) (*Manager, error) {
	cfgRoot, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("getting config dir: %w", err)
	}

	cacheRoot := filepath.Join(cfgRoot, "auth", "msal")
	if err := os.MkdirAll(cacheRoot, cacheDirectoryFileMode); err != nil {
		return nil, fmt.Errorf("creating cache root: %w", err)
	}

	publicClientApp, err := public.New(cAZD_CLIENT_ID, public.WithCache(newCache(cacheRoot)))
	if err != nil {
		return nil, fmt.Errorf("creating msal client: %w", err)
	}

	return &Manager{
		out:           out,
		publicClient:  &publicClientApp,
		configManager: configManager,
		cacheRoot:     cacheRoot,
	}, nil
}

var ErrNoCurrentUser = errors.New("not logged in, run `azd login` to login")

func (m *Manager) GetCredentialForCurrentUser(ctx context.Context) (azcore.TokenCredential, error) {
	_, cred, _, err := m.GetSignedInUser(ctx)
	return cred, err
}

func (m *Manager) GetSignedInUser(ctx context.Context) (*public.Account, azcore.TokenCredential, *time.Time, error) {
	cfg, err := config.GetUserConfig(m.configManager)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fetching current user: %w", err)
	}

	currentUser, has := cfg.Get(cCurrentUserKey)
	if !has {
		return nil, nil, nil, ErrNoCurrentUser
	}

	currentUserData, ok := currentUser.(map[string]any)
	if !ok {
		log.Println("current user data is corrupted, ignoring")
		return nil, nil, nil, ErrNoCurrentUser
	}

	if _, has := currentUserData["homeId"]; has {
		currentUserHomeId := currentUserData["homeId"].(string)

		for _, account := range m.publicClient.Accounts() {
			if account.HomeAccountID == currentUserHomeId {
				cred := m.newCredential(&account)
				if tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: cLoginScopes}); err != nil {
					return nil, nil, nil, fmt.Errorf("failed to get token: %v: %w", err, ErrNoCurrentUser)
				} else {
					return &account, m.newCredential(&account), &tok.ExpiresOn, nil
				}
			}

			log.Printf("ignoring cached account with home id '%s', does not match '%s'",
				account.HomeAccountID, currentUserHomeId)
		}
	} else if _, has := currentUserData["clientId"]; has {
		return m.LoginWithServicePrincipal(
			ctx,
			currentUserData["tenantId"].(string),
			currentUserData["clientId"].(string),
			currentUserData["clientSecret"].(string))
	}

	return nil, nil, nil, ErrNoCurrentUser
}

func (m *Manager) Login(
	ctx context.Context,
	useDeviceCode bool,
) (*public.Account, azcore.TokenCredential, *time.Time, error) {

	var authResult public.AuthResult

	if useDeviceCode {
		code, err := m.publicClient.AcquireTokenByDeviceCode(ctx, cLoginScopes)
		if err != nil {
			return nil, nil, nil, err
		}

		fmt.Fprintln(m.out, code.Result.Message)

		res, err := code.AuthenticationResult(ctx)
		if err != nil {
			return nil, nil, nil, err
		}

		authResult = res
	} else {
		res, err := m.publicClient.AcquireTokenInteractive(ctx, cLoginScopes)
		if err != nil {
			return nil, nil, nil, err
		}

		authResult = res
	}

	cfg, err := config.GetUserConfig(m.configManager)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fetching current user: %w", err)
	}

	if err := cfg.Set(cCurrentUserKey, map[string]any{"homeId": authResult.Account.HomeAccountID}); err != nil {
		return nil, nil, nil, fmt.Errorf("setting account id in config: %w", err)
	}

	userConfigFilePath, err := config.GetUserConfigFilePath()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed getting user config file path. %w", err)
	}

	if err := m.configManager.Save(cfg, userConfigFilePath); err != nil {
		return nil, nil, nil, fmt.Errorf("failed saving configuration: %w", err)
	}

	log.Printf("logged in as %s (%s)", authResult.Account.PreferredUsername, authResult.Account.HomeAccountID)

	return &authResult.Account, m.newCredential(&authResult.Account), &authResult.ExpiresOn, nil
}

func (m *Manager) LoginWithServicePrincipal(
	ctx context.Context, tenantId, clientId, clientSecret string,
) (*public.Account, azcore.TokenCredential, *time.Time, error) {

	cred, err := azidentity.NewClientSecretCredential(tenantId, clientId, clientSecret, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating credential: %w", err)
	}

	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: cLoginScopes,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fetching token: %w", err)
	}

	cfg, err := config.GetUserConfig(m.configManager)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fetching current user: %w", err)
	}

	if err := cfg.Set(cCurrentUserKey, map[string]any{
		"tenantId": tenantId,
		"clientId": clientId,
		// TODO(ellismg): We need to encrypt this...
		"clientSecret": clientSecret,
	}); err != nil {
		return nil, nil, nil, fmt.Errorf("setting account id in config: %w", err)
	}

	userConfigFilePath, err := config.GetUserConfigFilePath()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed getting user config file path. %w", err)
	}

	if err := m.configManager.Save(cfg, userConfigFilePath); err != nil {
		return nil, nil, nil, fmt.Errorf("failed saving configuration: %w", err)
	}

	return nil, cred, &tok.ExpiresOn, nil
}

func (m *Manager) Logout(ctx context.Context) error {
	act, _, _, err := m.GetSignedInUser(ctx)
	if errors.Is(err, ErrNoCurrentUser) {
		// already signed out, that's okay
		return nil
	} else if err != nil {
		return fmt.Errorf("fetching current user: %w", err)
	}

	// Unset the current user from config, but if we fail to do so, don't fail the overall operation
	if cfg, err := config.GetUserConfig(m.configManager); err != nil {
		log.Printf("error fetching config for current user during logout. ignoring: %v", err)
	} else {
		if err := cfg.Unset(cCurrentUserKey); err != nil {
			log.Printf("error un-setting key current user during logout. ignoring: %v", err)
		}
	}

	if act != nil {
		if err := m.publicClient.RemoveAccount(*act); err != nil {
			return fmt.Errorf("removing account from msal cache: %w", err)
		}
	}

	return nil
}

func (m *Manager) newCredential(a *public.Account) azcore.TokenCredential {
	return &azdCredential{
		client:  m.publicClient,
		account: a,
	}
}
