package auth

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/99designs/keyring"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

const cacheDirectoryFileMode = 0700
const cacheFileFileMode = 0600

const azdKeyringServiceName = "azd-auth"
const azdKeyringItemKey = "azd-auth-encryption-key"

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

var _ cache.ExportReplace = &msalCache{}

type msalCache struct {
	cache map[string][]byte
	inner cache.ExportReplace
}

type cacheUpdatingUnmarshaler struct {
	c     *msalCache
	key   string
	inner cache.Unmarshaler
}

func (r *cacheUpdatingUnmarshaler) Unmarshal(b []byte) error {
	r.c.cache[r.key] = b
	return r.inner.Unmarshal(b)
}

func (c *msalCache) Replace(cache cache.Unmarshaler, key string) {
	log.Printf("msalCache: replacing cache with key '%s'", key)

	if v, has := c.cache[key]; has {
		cache.Unmarshal(v)
	} else if c.inner != nil {
		c.inner.Replace(&cacheUpdatingUnmarshaler{
			c:     c,
			key:   key,
			inner: cache,
		}, key)
	} else {
		log.Printf("no existing cache entry found with key '%s'", key)
	}
}

func (c *msalCache) Export(cache cache.Marshaler, key string) {
	log.Printf("msalCache: exporting cache with key '%s'", key)

	new, err := cache.Marshal()
	if err != nil {
		log.Printf("error marshaling existing msal cache: %v", err)
		return
	}

	old := c.cache[key]

	if !bytes.Equal(old, new) {
		c.cache[key] = new
		c.inner.Export(cache, key)
	}
}

// encryptedCache is a cache.ExportReplace that uses encrypted files. The cache is stored in files named `cache%s.bin`
// (where %s is the 'key' parameter of the ExportReplace interface). The files are encrypted by AES-256-GCM using a key.
// The format of the file is "v1:<base-64-encoded-nonce>:<base-64-encoded-ciphertext>".
//
// TODO(ellismg): Scott said we should use a JWE here for persistance.
type encryptedCache struct {
	root string
	key  []byte
}

func (c *encryptedCache) Export(cache cache.Marshaler, key string) {
	log.Printf("encryptedCache: exporting cache with key '%s'", key)
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

func (c *encryptedCache) Replace(cache cache.Unmarshaler, key string) {
	log.Printf("encryptedCache: replacing cache with key '%s'", key)
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
