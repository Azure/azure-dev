package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const (
	cSubscriptionsCachePrefix = "subscriptions"
	cSubscriptionsCacheSuffix = ".cache"
	cSubscriptionsCacheGlob   = cSubscriptionsCachePrefix + "*" + cSubscriptionsCacheSuffix
)

// SubscriptionsCache caches the list of subscriptions accessible by the currently logged in account.
//
// The cache is backed by an in-memory copy, then by local file system storage.
// The cache key should be chosen to be unique to the user, such as the user's object ID.
type SubscriptionsCache struct {
	cacheDir string

	memoryCache map[string][]Subscription
	memoryLock  sync.RWMutex
}

func newSubCache() (*SubscriptionsCache, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("loading stored user subscriptions: %w", err)
	}

	return &SubscriptionsCache{
		cacheDir:    configDir,
		memoryCache: map[string][]Subscription{},
	}, nil
}

func (s *SubscriptionsCache) cachePath(key string) string {
	return filepath.Join(
		s.cacheDir,
		cSubscriptionsCachePrefix+"."+key+cSubscriptionsCacheSuffix)
}

// Load loads the subscriptions from cache with the key. Returns any error reading the cache.
func (s *SubscriptionsCache) Load(key string) ([]Subscription, error) {
	s.memoryLock.RLock()
	if res, ok := s.memoryCache[key]; ok {
		defer s.memoryLock.RUnlock()
		return res, nil
	}
	s.memoryLock.RUnlock()

	s.memoryLock.Lock()
	defer s.memoryLock.Unlock()
	cacheFile, err := os.ReadFile(s.cachePath(key))
	if err != nil {
		return nil, err
	}

	var subscriptions []Subscription
	err = json.Unmarshal(cacheFile, &subscriptions)
	if err != nil {
		return nil, err
	}

	s.memoryCache[key] = subscriptions
	return subscriptions, nil
}

// Save saves the subscriptions to cache with the specified key.
func (s *SubscriptionsCache) Save(key string, subscriptions []Subscription) error {
	s.memoryLock.Lock()
	defer s.memoryLock.Unlock()
	content, err := json.Marshal(subscriptions)
	if err != nil {
		return fmt.Errorf("failed to marshal subscriptions: %w", err)
	}

	err = os.WriteFile(s.cachePath(key), content, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	s.memoryCache[key] = subscriptions
	return err
}

// Clear removes all stored cache items. Returns an error if a filesystem error other than ErrNotExist occurred.
func (s *SubscriptionsCache) Clear() error {
	s.memoryLock.Lock()
	defer s.memoryLock.Unlock()

	matches, err := filepath.Glob(filepath.Join(s.cacheDir, cSubscriptionsCacheGlob))
	if err != nil {
		return err
	}

	for _, m := range matches {
		err = os.Remove(m)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	s.memoryCache = map[string][]Subscription{}
	return nil
}
