// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
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

// cUseAzCli is the key we use in config to denote that we want to use the az CLI for authentication instead of managing
// it ourselves. The value should be a string as specified by [strconv.ParseBool]
const cUseLegacyAzCliAuthKey = "auth.useLegacyAzCliAuth"

// The scopes to request when acquiring our token during the login flow.
var cLoginScopes = []string{"https://management.azure.com//.default"}

// authDirectoryFileMode is the file mode used to create the folder that is used for auth folder and sub-folders.
const authDirectoryFileMode = 0700

type Manager struct {
	out             io.Writer
	publicClient    *public.Client
	configManager   config.Manager
	credentialCache exportReplaceWithErrors
}

func NewManager(out io.Writer, configManager config.Manager) (*Manager, error) {
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
		out:             out,
		publicClient:    &publicClientApp,
		configManager:   configManager,
		credentialCache: newCredentialCache(authRoot),
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

	if useLegacyAuth, has := cfg.Get(cUseLegacyAzCliAuthKey); has {
		if use, err := strconv.ParseBool(useLegacyAuth.(string)); err != nil && use {
			cred, err := azidentity.NewAzureCLICredential(nil)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to create credential: %v: %w", err, ErrNoCurrentUser)
			}
			if tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: cLoginScopes}); err != nil {
				return nil, nil, nil, fmt.Errorf("failed to get token: %v: %w", err, ErrNoCurrentUser)
			} else {
				return nil, cred, &tok.ExpiresOn, nil
			}
		}
	}

	currentUser, has := cfg.Get(cCurrentUserKey)
	if !has {
		return nil, nil, nil, ErrNoCurrentUser
	}

	currentUserData := currentUser.(map[string]any)

	if _, has := currentUserData["homeId"]; has {
		currentUserHomeId := currentUserData["homeId"].(string)

		for _, account := range m.publicClient.Accounts() {
			if account.HomeAccountID == currentUserHomeId {
				cred := newAzdCredential(m.publicClient, &account)
				if tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: cLoginScopes}); err != nil {
					return nil, nil, nil, fmt.Errorf("failed to get token: %v: %w", err, ErrNoCurrentUser)
				} else {
					return &account, cred, &tok.ExpiresOn, nil
				}
			}

			log.Printf("ignoring cached account with home id '%s', does not match '%s'",
				account.HomeAccountID, currentUserHomeId)
		}
	} else if _, has := currentUserData["clientId"]; has {
		clientId := currentUserData["clientId"].(string)
		tenantId := currentUserData["tenantId"].(string)

		ps, err := m.loadSecret(persistedSecretLookupKey(tenantId, clientId))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("loading secret: %v: %w", err, ErrNoCurrentUser)
		}

		cred, err := azidentity.NewClientSecretCredential(tenantId, clientId, *ps.ClientSecret, nil)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("creating credential: %v: %w", err, ErrNoCurrentUser)
		}

		if tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: cLoginScopes}); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to get token: %v: %w", err, ErrNoCurrentUser)
		} else {
			return nil, cred, &tok.ExpiresOn, nil
		}
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

		// Display the message to the end user as to what to do next, then block waiting for them to complete
		// the flow.
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

	if err := m.saveCurrentUserProperties(map[string]any{"homeId": authResult.Account.HomeAccountID}); err != nil {
		return nil, nil, nil, err
	}

	log.Printf("logged in as %s (%s)", authResult.Account.PreferredUsername, authResult.Account.HomeAccountID)

	return &authResult.Account, newAzdCredential(m.publicClient, &authResult.Account), &authResult.ExpiresOn, nil
}

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

	if err := m.saveSecret(
		&persistedSecret{
			ClientSecret: &clientSecret,
		},
		persistedSecretLookupKey(tenantId, clientId),
	); err != nil {
		return nil, nil, nil, err
	}

	if err := m.saveCurrentUserProperties(map[string]any{
		"tenantId": tenantId,
		"clientId": clientId,
	}); err != nil {
		return nil, nil, nil, err
	}

	return nil, cred, &tok.ExpiresOn, nil
}

func persistedSecretLookupKey(tenantId, clientId string) string {
	return fmt.Sprintf("%s.%s", tenantId, clientId)
}

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

func (m *Manager) saveSecret(ps *persistedSecret, key string) error {
	data, err := json.Marshal(ps)
	if err != nil {
		return err
	}

	return m.credentialCache.Export(&fixedMarshaller{val: data}, key)
}

func (m *Manager) Logout(ctx context.Context) error {
	act, _, _, err := m.GetSignedInUser(ctx)
	if errors.Is(err, ErrNoCurrentUser) {
		// already signed out, that's okay
		return nil
	} else if err != nil {
		return fmt.Errorf("fetching current user: %w", err)
	}

	if act != nil {
		if err := m.publicClient.RemoveAccount(*act); err != nil {
			return fmt.Errorf("removing account from msal cache: %w", err)
		}
	}

	// Unset the current user from config, but if we fail to do so, don't fail the overall operation
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

			if err := m.saveSecret(&persistedSecret{}, persistedSecretLookupKey(tenantId, clientId)); err != nil {
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

type persistedSecret struct {
	ClientSecret *string `json:"clientSecret,omitempty"`
}
