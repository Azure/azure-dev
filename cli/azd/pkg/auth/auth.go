package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/99designs/keyring"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// nolint:lll
// https://github.com/Azure/azure-cli/blob/6cd8cedd49e2546b57673b4ab6ebd7567b73e530/src/azure-cli-core/azure/cli/core/auth/identity.py#L23
const cAZURE_CLI_CLIENT_ID = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"

const cacheDirectoryFileMode = 0700
const cacheFileFileMode = 0600

const azdKeyringServiceName = "azd-auth"
const azdKeyringItemKey = "azd-auth-encryption-key"

// The scopes to request when acquiring our token during the login flow.
//
// TODO(ellismg): Should we be requesting additional scopes, like the scopes for the data plane operations we need to do
// (for example Key Vault?)
var LoginScopes = []string{"https://management.azure.com/.default"}

var _ azcore.TokenCredential = &azdCredential{}

type azdCredential struct {
	client  *public.Client
	account *public.Account
}

func (c *azdCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	log.Printf("fetching token scopes for account %s with scopes %+v", c.account.HomeAccountID, options.Scopes)
	res, err := c.client.AcquireTokenSilent(ctx, options.Scopes, public.WithSilentAccount(*c.account))
	log.Printf("token fetch completed, err=%v", err)

	if err != nil {
		return azcore.AccessToken{}, err
	}

	return azcore.AccessToken{
		Token:     res.AccessToken,
		ExpiresOn: res.ExpiresOn.UTC(),
	}, nil
}

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

	publicClientApp, err := public.New(cAZURE_CLI_CLIENT_ID, public.WithCache(&encryptedCache{
		root: cacheRoot,
		key:  key,
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

// getCacheKey gets the encryption key used to encrypt the MSAL cache from the system keyring. If a key does not already
// exist, a new one is generated and stored in the system keyring.
func getCacheKey() ([]byte, error) {
	ring, err := keyring.Open(keyring.Config{
		ServiceName:     azdKeyringServiceName,
		AllowedBackends: azdKeyringAllowedBackends,
	})
	if err != nil {
		return nil, fmt.Errorf("opening keyring: %w", err)
	}

	item, err := ring.Get(azdKeyringItemKey)
	if err != nil && !errors.Is(err, keyring.ErrKeyNotFound) {
		return nil, fmt.Errorf("reading secret: %w", err)
	} else if errors.Is(err, keyring.ErrKeyNotFound) {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}

		item = keyring.Item{
			Key:  azdKeyringItemKey,
			Data: buf,
		}
		if err := ring.Set(item); err != nil {
			return nil, fmt.Errorf("writing secret: %w", err)
		}
	}

	return item.Data, nil
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

var _ cache.ExportReplace = &encryptedCache{}

// encryptedCache is a cache.ExportReplace that uses encrypted files. The cache is stored in files named `cache%s.bin`
// (where %s is the 'key' parameter of the ExportReplace interface). The files are encrypted by AES-256-GCM using a key.
// The format of the file is "v1:<base-64-encoded-nonce>:<base-64-encoded-ciphertext>".
//
// TODO(ellismg): Scott said we should use a JWE here for persistance.
type encryptedCache struct {
	root string
	key  []byte
}

func (c *encryptedCache) Replace(cache cache.Unmarshaler, key string) {
	log.Printf("replacing cache with key '%s'", key)
	cacheFile := filepath.Join(c.root, fmt.Sprintf("cache%s.bin", key))

	contents, err := os.ReadFile(cacheFile)
	if err != nil {
		log.Printf("failed to read encrypted cache file: %s: %v", cacheFile, err)
		return
	}

	parts := strings.Split(string(contents), ":")
	if len(parts) != 3 || parts[0] != "v1" {
		log.Printf("incorrect format for encrypted cache file: %s: %v", cacheFile, err)
		return
	}

	nonce, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		log.Printf("failed to base64 decode nonce: %v", err)
		return
	}

	ciphertext, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		log.Printf("failed to base64 decode ciphertext: %v", err)
		return
	}

	decrypted, err := c.decrypt(nonce, ciphertext)
	if err != nil {
		log.Printf("failed decrypt cache: %v", err)
		return
	}

	cache.Unmarshal(decrypted)
}

func (c *encryptedCache) decrypt(nonce []byte, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		panic(err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}

	return gcm.Open(nil, nonce, ciphertext, nil)
}

func (c *encryptedCache) Export(cache cache.Marshaler, key string) {
	log.Printf("exporting cache with key '%s'", key)
	res, err := cache.Marshal()
	if err != nil {
		panic(err)
	}

	block, err := aes.NewCipher(c.key)
	if err != nil {
		panic(err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}

	nonce := make([]byte, gcm.NonceSize())

	if _, err := rand.Read(nonce); err != nil {
		log.Printf("failed to generate nonce, not caching")
	}

	ciphertext := gcm.Seal(nil, nonce, res, nil)

	fileContents := fmt.Sprintf(
		"v1:%s:%s",
		base64.StdEncoding.EncodeToString(nonce),
		base64.StdEncoding.EncodeToString(ciphertext))

	cachePath := filepath.Join(c.root, fmt.Sprintf("cache%s.bin", key))

	if err := os.WriteFile(cachePath, []byte(fileContents), cacheFileFileMode); err != nil {
		panic(err)
	}
}
