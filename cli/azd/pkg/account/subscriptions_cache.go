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

// The file name of the cache used for storing subscriptions accessible by the currently logged in account.
const cSubscriptionsCacheFile = "subscriptions.cache"

// SubscriptionsCache caches a subscription to tenant access mapping
// for the logged in account to access each subscription.
//
// The cache is backed by an in-memory copy, then by local file system storage.
type SubscriptionsCache struct {
	cachePath string

	inMemoryCopy []Subscription
	inMemoryLock sync.RWMutex
}

func NewSubscriptionsCache() (*SubscriptionsCache, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("loading stored user subscriptions: %w", err)
	}

	return NewSubscriptionsCacheWithDir(filepath.Join(configDir, cSubscriptionsCacheFile))
}

func NewSubscriptionsCacheWithDir(cachePath string) (*SubscriptionsCache, error) {
	return &SubscriptionsCache{
		cachePath: cachePath,
	}, nil
}

// Load loads the subscriptions from cache.
func (s *SubscriptionsCache) Load() ([]Subscription, error) {
	s.inMemoryLock.RLock()
	if s.inMemoryCopy != nil {
		defer s.inMemoryLock.RUnlock()
		return s.inMemoryCopy, nil
	}
	s.inMemoryLock.RUnlock()

	s.inMemoryLock.Lock()
	defer s.inMemoryLock.Unlock()
	cacheFile, err := os.ReadFile(s.cachePath)
	if err != nil {
		return nil, err
	}

	var subscriptions []Subscription
	err = json.Unmarshal(cacheFile, &subscriptions)
	if err != nil {
		return nil, err
	}

	s.inMemoryCopy = subscriptions
	return subscriptions, nil
}

// Save saves the subscriptions to cache.
func (s *SubscriptionsCache) Save(subscriptions []Subscription) error {
	s.inMemoryLock.Lock()
	defer s.inMemoryLock.Unlock()
	content, err := json.Marshal(subscriptions)
	if err != nil {
		return fmt.Errorf("failed to marshal subscriptions: %w", err)
	}

	err = os.WriteFile(s.cachePath, content, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	s.inMemoryCopy = subscriptions
	return err
}

func (s *SubscriptionsCache) Clear() error {
	s.inMemoryLock.Lock()
	defer s.inMemoryLock.Unlock()

	err := os.Remove(s.cachePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	s.inMemoryCopy = []Subscription{}
	return nil
}
