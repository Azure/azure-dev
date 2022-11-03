// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/gofrs/flock"
)

// fileCache implements exportReplaceWithErrors by storing the data to disk.  The cache key is used as part of the
// filename for the stored object. Files are stored in [root] and are named [prefix][key].[ext].
type fileCache struct {
	prefix string
	root   string
	ext    string
}

func (c *fileCache) Replace(cache cache.Unmarshaler, key string) error {
	contents, err := c.readCacheWithLock(key)
	if err != nil {
		return fmt.Errorf("failed to read cache: %w", err)
	}

	if err := cache.Unmarshal(contents); err != nil {
		return fmt.Errorf("failed to unmarshal cache: %w", err)
	}

	return nil
}

func (c *fileCache) Export(cache cache.Marshaler, key string) error {
	new, err := cache.Marshal()
	if err != nil {
		return fmt.Errorf("error marshaling existing msal cache: %w", err)
	}

	if err := c.writeFileWithLock(key, new); err != nil {
		return fmt.Errorf("failed to write cache: %w", err)
	}

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
	defer func() {
		if err := fl.Unlock(); err != nil {
			log.Printf("failed to release file lock: %v", err)
		}
	}()

	return os.ReadFile(cachePath)
}

// writeFileWithLock writes the cache file for a given key. The write is guarded by
// a file lock.
func (c *fileCache) writeFileWithLock(key string, data []byte) error {
	cachePath := c.pathForCache(key)
	lockPath := c.pathForLock(key)

	fl := flock.New(lockPath)

	if err := fl.Lock(); err != nil {
		return fmt.Errorf("locking file %s: %w", lockPath, err)
	}
	defer func() {
		if err := fl.Unlock(); err != nil {
			log.Printf("failed to release file lock: %v", err)
		}
	}()

	return os.WriteFile(cachePath, data, osutil.PermissionFileOwnerOnly)
}

func (c *fileCache) pathForCache(key string) string {
	return filepath.Join(c.root, fmt.Sprintf("%s%s.%s", c.prefix, key, c.ext))
}

func (c *fileCache) pathForLock(key string) string {
	return filepath.Join(c.root, fmt.Sprintf("%s%s.%s.lock", c.prefix, key, c.ext))
}
