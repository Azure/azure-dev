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
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// TODO(azure/azure-dev#710): Right now, we re-use the App Id of the `az` CLI, until we have our own.
//
// nolint:lll
// https://github.com/Azure/azure-cli/blob/azure-cli-2.41.0/src/azure-cli-core/azure/cli/core/auth/identity.py#L23
const cAZD_CLIENT_ID = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"

// The scopes to request when acquiring our token during the login flow.
var cLoginScopes = []string{"https://management.azure.com/.default"}

// cacheDirectoryFileMode is the file mode used to create the folder that is used for the MSAL cache.
const cacheDirectoryFileMode = 0700

type Manager struct {
	out    io.Writer
	client *public.Client
}

func NewManager(out io.Writer) (*Manager, error) {
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
		client: &publicClientApp,
		out:    out,
	}, nil
}

var ErrNoCurrentUser = errors.New("not logged in, run `azd login` to login")

func (m *Manager) GetSignedInUser(ctx context.Context) (*public.Account, azcore.TokenCredential, *time.Time, error) {
	cfg, err := config.Load()
	if errors.Is(err, os.ErrNotExist) {
		cfg = &config.Config{}
	} else if err != nil {
		return nil, nil, nil, fmt.Errorf("fetching current user: %w", err)
	}

	if cfg.Account == nil || cfg.Account.CurrentUserHomeId == nil {
		return nil, nil, nil, ErrNoCurrentUser
	}

	for _, account := range m.client.Accounts() {
		if account.HomeAccountID == *cfg.Account.CurrentUserHomeId {
			cred := m.newCredential(&account)
			if tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: cLoginScopes}); err != nil {
				return nil, nil, nil, fmt.Errorf("failed to get token: %v: %w", err, ErrNoCurrentUser)
			} else {
				return &account, m.newCredential(&account), &tok.ExpiresOn, nil
			}
		}

		log.Printf("ignoring cached account with home id '%s', does not match '%s'",
			account.HomeAccountID, *cfg.Account.CurrentUserHomeId)
	}

	return nil, nil, nil, ErrNoCurrentUser
}

func (m *Manager) Login(
	ctx context.Context,
	useDeviceCode bool,
) (*public.Account, azcore.TokenCredential, *time.Time, error) {

	var authResult public.AuthResult

	if useDeviceCode {
		code, err := m.client.AcquireTokenByDeviceCode(ctx, cLoginScopes)
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
		res, err := m.client.AcquireTokenInteractive(ctx, cLoginScopes)
		if err != nil {
			return nil, nil, nil, err
		}

		authResult = res
	}

	cfg, err := config.Load()
	if errors.Is(err, os.ErrNotExist) {
		cfg = &config.Config{}
	} else if err != nil {
		return nil, nil, nil, fmt.Errorf("loading config: %w", err)
	}

	if cfg.Account == nil {
		cfg.Account = &config.Account{}
	}

	cfg.Account.CurrentUserHomeId = &authResult.Account.HomeAccountID

	if err := cfg.Save(); err != nil {
		return nil, nil, nil, fmt.Errorf("saving config: %w", err)
	}

	log.Printf("logged in as %s (%s)", authResult.Account.PreferredUsername, authResult.Account.HomeAccountID)

	return &authResult.Account, m.newCredential(&authResult.Account), &authResult.ExpiresOn, nil
}

func (m *Manager) newCredential(a *public.Account) azcore.TokenCredential {
	return &azdCredential{
		client:  m.client,
		account: a,
	}
}

func saveCurrentUser(homeId string) error {
	cfg, err := config.Load()
	if errors.Is(err, os.ErrNotExist) {
		cfg = &config.Config{}
	} else if err != nil {
		return err
	}

	if cfg.Account == nil {
		cfg.Account = &config.Account{}
	}

	cfg.Account.CurrentUserHomeId = &homeId

	if err := cfg.Save(); err != nil {
		return err
	}

	return nil
}
