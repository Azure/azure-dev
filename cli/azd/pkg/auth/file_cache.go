// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/gofrs/flock"
)

const cacheFileFileMode = 0600

var _ cache.ExportReplace = &fileCache{}

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

	return os.WriteFile(cachePath, data, cacheFileFileMode)
}

func (c *fileCache) pathForCache(key string) string {
	return filepath.Join(c.root, fmt.Sprintf("cache%s.bin", key))
}

func (c *fileCache) pathForLock(key string) string {
	return filepath.Join(c.root, fmt.Sprintf("cache%s.lock", key))
}
