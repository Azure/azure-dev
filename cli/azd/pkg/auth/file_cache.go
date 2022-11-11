// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/gofrs/flock"
)

// fileCache implements Cache by storing the data to disk. The cache key is used as part of the
// filename for the stored object. Files are stored in [root] and are named [prefix][key].[ext].
type fileCache struct {
	prefix string
	root   string
	ext    string
}

func (c *fileCache) Read(key string) ([]byte, error) {
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

func (c *fileCache) Set(key string, value []byte) error {
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

	return os.WriteFile(cachePath, value, osutil.PermissionFileOwnerOnly)
}

func (c *fileCache) pathForCache(key string) string {
	return filepath.Join(c.root, fmt.Sprintf("%s%s.%s", c.prefix, key, c.ext))
}

func (c *fileCache) pathForLock(key string) string {
	return filepath.Join(c.root, fmt.Sprintf("%s%s.%s.lock", c.prefix, key, c.ext))
}
