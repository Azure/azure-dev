package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// The cache used for storing subscriptions accessible by the currently logged in user.
const cSubscriptionsCacheFile = "subscriptions.cache"

type SubscriptionsCache struct {
	cachePath string
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
	cacheFile, err := os.ReadFile(s.cachePath)
	if err != nil {
		return nil, err
	}

	var subscriptions []Subscription
	err = json.Unmarshal(cacheFile, &subscriptions)
	if err != nil {
		return nil, err
	}

	return subscriptions, nil
}

// Save saves the subscriptions to cache.
func (s *SubscriptionsCache) Save(subscriptions []Subscription) error {
	content, err := json.Marshal(subscriptions)
	if err != nil {
		return fmt.Errorf("failed to marshal subscriptions: %w", err)
	}

	err = os.WriteFile(s.cachePath, content, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return err
}
