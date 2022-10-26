package auth

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/99designs/keyring"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/gofrs/flock"
	"gopkg.in/square/go-jose.v2"
)

const cacheFileFileMode = 0600

const azdKeyringServiceName = "azd-auth"
const azdKeyringItemKey = "azd-auth-encryption-key"

// getCacheKey gets the encryption key used to encrypt the MSAL cache from the system keyring. If a key does not already
// exist, a new one is generated and stored in the system keyring.
func getCacheKey() ([]byte, error) {
	ring, err := keyring.Open(keyring.Config{
		ServiceName:     azdKeyringServiceName,
		AllowedBackends: azdKeyringAllowedBackends,
		KeychainName:    "login",
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

var _ cache.ExportReplace = &fileCache{}
var _ cache.ExportReplace = &encryptedCache{}
var _ cache.ExportReplace = &memoryCache{}

type fileCache struct {
	root string
}

func (c *fileCache) Replace(cache cache.Unmarshaler, key string) {
	log.Printf("fileCache: replacing cache with key '%s'", key)

	contents, err := c.readCacheWithLock(key)
	if err != nil {
		log.Printf("failed to read cache: %v", err)
		return
	}

	if err := cache.Unmarshal(contents); err != nil {
		log.Printf("failed to unmarshal cache: %v", err)
		return
	}
}

func (c *fileCache) Export(cache cache.Marshaler, key string) {
	log.Printf("fileCache: exporting cache with key '%s'", key)

	new, err := cache.Marshal()
	if err != nil {
		log.Printf("error marshaling existing msal cache: %v", err)
		return
	}

	if err := c.writeFileWithLock(key, new); err != nil {
		log.Printf("failed to write cache: %v", err)
		return
	}
}

var _ cache.Marshaler = &fixedMarshaller{}
var _ cache.Unmarshaler = &fixedMarshaller{}

type fixedMarshaller struct {
	val []byte
}

func (f *fixedMarshaller) Marshal() ([]byte, error) {
	return f.val, nil
}

func (f *fixedMarshaller) Unmarshal(cache []byte) error {
	f.val = cache
	return nil
}

// readCacheWithLock reads the cache file for a given key. The read is guarded by
// a file lock.
func (c *fileCache) readCacheWithLock(key string) ([]byte, error) {
	cachePath := c.pathForCache(key)
	lockPath := c.pathForLock(key)

	fl := flock.New(lockPath)

	if err := fl.Lock(); err != nil {
		return nil, fmt.Errorf("locking file %s: %w", lockPath, err)
	}
	defer fl.Unlock()

	return os.ReadFile(cachePath)
}

func (c *fileCache) writeFileWithLock(key string, data []byte) error {
	cachePath := c.pathForCache(key)
	lockPath := c.pathForLock(key)

	fl := flock.New(lockPath)

	if err := fl.Lock(); err != nil {
		return fmt.Errorf("locking file %s: %w", lockPath, err)
	}
	defer fl.Unlock()

	return os.WriteFile(cachePath, data, cacheFileFileMode)
}

func (c *fileCache) pathForCache(key string) string {
	return filepath.Join(c.root, fmt.Sprintf("cache%s.bin", key))
}

func (c *fileCache) pathForLock(key string) string {
	return filepath.Join(c.root, fmt.Sprintf("cache%s.lock", key))
}

// memoryCache is a simple memory cache that implements cache.ExportReplace. During export, if the cache
// contents has not changed, the nested cache is not notified of a change.
type memoryCache struct {
	cache map[string][]byte
	inner cache.ExportReplace
}

// cacheUpdatingUnmarshaler implements cache.Unmarshaler. During unmarshalling it updates the value in the memory
// cache and then forwards the call to the next unmarshaler (which will typically update MSAL's internal cache).
type cacheUpdatingUnmarshaler struct {
	c     *memoryCache
	key   string
	inner cache.Unmarshaler
}

func (r *cacheUpdatingUnmarshaler) Unmarshal(b []byte) error {
	r.c.cache[r.key] = b
	return r.inner.Unmarshal(b)
}

func (c *memoryCache) Replace(cache cache.Unmarshaler, key string) {
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

func (c *memoryCache) Export(cache cache.Marshaler, key string) {
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
// (where %s is the 'key' parameter of the ExportReplace interface). The files are encrypted using JSON Web Encryption
// with AES-256-GCM and stored stored using the compact encoding. Mutual exclusion is provided using file locking with
// a file named `cache%s.lock`.
type encryptedCache struct {
	key   []byte
	inner cache.ExportReplace
}

func (c *encryptedCache) Export(cache cache.Marshaler, key string) {
	log.Printf("encryptedCache: exporting cache with key '%s'", key)
	res, err := cache.Marshal()
	if err != nil {
		fmt.Printf("failed to marshal cache from MSAL: %v", err)
		return
	}

	encrypter, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{
		Algorithm: jose.DIRECT,
		Key:       c.key,
	}, nil)
	if err != nil {
		fmt.Printf("failed to create JWE Encrypter: %v", err)
		return
	}

	enc, err := encrypter.Encrypt(res)
	if err != nil {
		fmt.Printf("failed to create encrypt cache: %v", err)
		return
	}

	cs, err := enc.CompactSerialize()
	if err != nil {
		fmt.Printf("failed to serialized JWE: %v", err)
		return
	}

	c.inner.Export(&fixedMarshaller{
		val: []byte(cs),
	}, key)
}

func (c *encryptedCache) Replace(cache cache.Unmarshaler, key string) {
	log.Printf("encryptedCache: replacing cache with key '%s'", key)

	capture := &fixedMarshaller{}
	c.inner.Replace(capture, key)

	if len(capture.val) == 0 {
		log.Printf("encrypted cache is empty, ignoring")
		return
	}

	jwe, err := jose.ParseEncrypted(string(capture.val))
	if err != nil {
		log.Printf("failed to parse cache as a JWE, ignoring cache: %v", err)
		return
	}

	decrypted, err := jwe.Decrypt(c.key)
	if err != nil {
		log.Printf("failed to decrypt cache, ignoring cache: %v", err)
		return
	}

	if err := cache.Unmarshal(decrypted); err != nil {
		log.Printf("failed to unmarshal decrypted cache to MSAL: %v", err)
		return
	}
}
