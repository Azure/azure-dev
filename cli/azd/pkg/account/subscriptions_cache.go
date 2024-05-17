package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// The file name of the cache used for storing subscriptions accessible by local accounts.
const cSubscriptionsCacheFile = "subscriptions.cache"

// subscriptionsCache caches the list of subscriptions accessible by local accounts.
//
// The cache is backed by an in-memory copy, then by local file system storage.
// The cache key should be chosen to be unique to the user, such as the user's object ID.
//
// To clear all entries in the cache, call Clear().
type subscriptionsCache struct {
	cacheDir string

	inMemoryCopy map[string][]Subscription
	inMemoryLock sync.RWMutex
}

func newSubCache() (*subscriptionsCache, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("loading stored user subscriptions: %w", err)
	}

	return &subscriptionsCache{
		cacheDir:     configDir,
		inMemoryCopy: map[string][]Subscription{},
	}, nil
}

// Load loads the subscriptions from cache with the key. Returns any error reading the cache.
func (s *subscriptionsCache) Load(ctx context.Context, key string) ([]Subscription, error) {
	// check in-memory cache
	s.inMemoryLock.RLock()
	if res, ok := s.inMemoryCopy[key]; ok {
		defer s.inMemoryLock.RUnlock()
		return res, nil
	}
	s.inMemoryLock.RUnlock()

	s.inMemoryLock.Lock()
	defer s.inMemoryLock.Unlock()

	// load cache from disk
	cacheFile, err := os.ReadFile(filepath.Join(s.cacheDir, cSubscriptionsCacheFile))
	if err != nil {
		return nil, err
	}

	var cache map[string][]Subscription
	err = json.Unmarshal(cacheFile, &cache)
	if err != nil {
		return nil, err
	}
	s.inMemoryCopy = cache

	// return the key requested
	if res, ok := cache[key]; ok {
		return res, nil
	}

	return nil, os.ErrNotExist
}

// Save saves the subscriptions to cache with the specified key.
func (s *subscriptionsCache) Save(ctx context.Context, key string, subscriptions []Subscription) error {
	s.inMemoryLock.Lock()
	defer s.inMemoryLock.Unlock()

	// Read the file if it exists
	cacheFile, err := os.ReadFile(filepath.Join(s.cacheDir, cSubscriptionsCacheFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// unmarshal cache, ignoring the error if the cache was upgraded or corrupted
	cache := map[string][]Subscription{}
	if cacheFile != nil {
		err = json.Unmarshal(cacheFile, &cache)
		if err != nil {
			log.Printf("failed to unmarshal %s, ignoring: %v", cSubscriptionsCacheFile, err)
		}
	}

	// apply the update
	cache[key] = subscriptions

	// save new cache
	content, err := json.Marshal(cache)
	if err != nil {
		return fmt.Errorf("failed to marshal subscriptions: %w", err)
	}

	err = os.WriteFile(filepath.Join(s.cacheDir, cSubscriptionsCacheFile), content, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	s.inMemoryCopy = cache
	return err
}

// Clear removes all stored cache items. Returns an error if a filesystem error other than ErrNotExist occurred.
func (s *subscriptionsCache) Clear(ctx context.Context) error {
	s.inMemoryLock.Lock()
	defer s.inMemoryLock.Unlock()

	err := os.Remove(filepath.Join(s.cacheDir, cSubscriptionsCacheFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	s.inMemoryCopy = map[string][]Subscription{}
	return nil
}
