package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// TODO(azure/azure-dev#710): Right now, we re-use the App Id of the `az` CLI, until we have our own.
//
// nolint:lll
// https://github.com/Azure/azure-cli/blob/azure-cli-2.41.0/src/azure-cli-core/azure/cli/core/auth/identity.py#L23
const cAZD_CLIENT_ID = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"

// The scopes to request when acquiring our token during the login flow.
//
// TODO(ellismg): Should we be requesting additional scopes, like the scopes for the data plane operations we need to do
// (for example Key Vault?)
var LoginScopes = []string{"https://management.azure.com/.default"}

type Manager struct {
	out    io.Writer
	client *public.Client
}

func NewManager(out io.Writer) (Manager, error) {
	key, err := getCacheKey()
	if err != nil {
		return Manager{}, fmt.Errorf("getting secret: %w", err)
	}

	cfgRoot, err := config.GetUserConfigDir()
	if err != nil {
		return Manager{}, fmt.Errorf("getting config dir: %w", err)
	}

	cacheRoot := filepath.Join(cfgRoot, "auth", "msal")
	if err := os.MkdirAll(cacheRoot, 0700); err != nil {
		return Manager{}, fmt.Errorf("creating cache root: %w", err)
	}

	publicClientApp, err := public.New(cAZD_CLIENT_ID, public.WithCache(&msalCache{
		cache: make(map[string][]byte),
		inner: &encryptedCache{
			root: cacheRoot,
			key:  key,
		},
	}))
	if err != nil {
		return Manager{}, fmt.Errorf("creating msal client: %w", err)
	}

	return Manager{
		client: &publicClientApp,
		out:    out,
	}, nil
}

var ErrNoCurrentUser = errors.New("not logged in, run `azd login` to login")

// TODO(ellismg): Should this also fetch an access token to ensure everything is "ok"?  If so,
// perhaps it can be returned and `login.go` can use it instead of calling GetToken itself?
func (m *Manager) CurrentAccount(ctx context.Context) (*public.Account, azcore.TokenCredential, error) {
	cfg, err := config.Load()
	if errors.Is(err, os.ErrNotExist) {
		cfg = &config.Config{}
	} else if err != nil {
		return nil, nil, fmt.Errorf("fetching current user: %w", err)
	}

	if cfg.Account == nil || cfg.Account.CurrentUserHomeId == nil {
		return nil, nil, ErrNoCurrentUser
	}

	for _, account := range m.client.Accounts() {
		if account.HomeAccountID == *cfg.Account.CurrentUserHomeId {
			cred := m.newCredential(&account)
			if _, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: LoginScopes}); err != nil {
				// TODO(ellismg): Feels like we need a real error type here...
				return nil, nil, fmt.Errorf("failed to get token: %v: %w", err, ErrNoCurrentUser)
			}

			return &account, m.newCredential(&account), nil
		}

		log.Printf("ignoring cached account with home id '%s', does not match '%s'",
			account.HomeAccountID, *cfg.Account.CurrentUserHomeId)
	}

	return nil, nil, ErrNoCurrentUser
}

func (m *Manager) Login(ctx context.Context, useDeviceCode bool) (*public.Account, azcore.TokenCredential, error) {
	var account public.Account

	if useDeviceCode {
		code, err := m.client.AcquireTokenByDeviceCode(ctx, LoginScopes)
		if err != nil {
			return nil, nil, err
		}

		fmt.Fprintln(m.out, code.Result.Message)

		res, err := code.AuthenticationResult(ctx)
		if err != nil {
			return nil, nil, err
		}

		account = res.Account
	} else {
		res, err := m.client.AcquireTokenInteractive(ctx, LoginScopes)
		if err != nil {
			return nil, nil, err
		}

		account = res.Account
	}

	cfg, err := config.Load()
	if errors.Is(err, os.ErrNotExist) {
		cfg = &config.Config{}
	} else if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w", err)
	}

	if cfg.Account == nil {
		cfg.Account = &config.Account{}
	}

	cfg.Account.CurrentUserHomeId = &account.HomeAccountID

	if err := cfg.Save(); err != nil {
		return nil, nil, fmt.Errorf("saving config: %w", err)
	}

	log.Printf("logged in as %s (%s)", account.PreferredUsername, account.HomeAccountID)

	return &account, m.newCredential(&account), nil
}

func (m *Manager) newCredential(a *public.Account) azcore.TokenCredential {
	return &azdCredential{
		client:  m.client,
		account: a,
	}
}
