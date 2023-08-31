// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"golang.org/x/exp/slices"
)

type BicepCache struct {
	Template   json.RawMessage
	Parameters azure.ArmParameters
}

type CacheManager interface {
	// Current returns existing cache or nil when there's no cache.
	Current(context context.Context) *BicepCache
	// Cache persist cache. Use Cache(nil) to clear cache.
	Cache(context context.Context, cache *BicepCache) error
	// Equal compares cache against current and return false when they are different.
	Equal(context context.Context, cache *BicepCache) bool
}

// bicepEnvCache writes a cache file inside the current azd environment.
type bicepEnvCache struct {
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]
}

const c_cacheFileName = "bicep.cache"

func (b *bicepEnvCache) Current(context context.Context) *BicepCache {
	cachePath, err := b.cachePath()
	if err != nil {
		return nil
	}
	_, err = os.Stat(cachePath)
	if err != nil {
		// no cache or can't read
		return nil
	}

	cacheContent, err := os.ReadFile(cachePath)
	if err != nil {
		// can't read cache is the same as no cache
		return nil
	}

	var cache BicepCache
	if err := json.Unmarshal(cacheContent, &cache); err != nil {
		// can't parse cache back to struct. void cache
		return nil
	}

	return &cache
}

func (b *bicepEnvCache) Cache(context context.Context, cache *BicepCache) error {
	cachePath, err := b.cachePath()
	if err != nil {
		return err
	}
	cacheContent, err := json.Marshal(cache)
	if err != nil {
		return fmt.Errorf("saving bicep cache %w", err)
	}

	_ = os.Remove(cachePath)
	err = os.WriteFile(cachePath, cacheContent, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("saving bicep cache %w", err)
	}
	return nil
}

func (b *bicepEnvCache) Equal(context context.Context, cache *BicepCache) bool {
	// cache is saved w/o format, hence, the comparison needs to be the same way
	currentCache, _ := json.Marshal(b.Current(context))
	rawCache, _ := json.Marshal(cache)
	return slices.Equal(currentCache, rawCache)
}

func (b *bicepEnvCache) cachePath() (string, error) {
	azdcontext, err := b.lazyAzdContext.GetValue()
	if err != nil {
		return "", err
	}
	envPath := azdcontext.EnvironmentDirectory()
	envName, err := azdcontext.GetDefaultEnvironmentName()
	if err != nil {
		return "", err
	}
	return filepath.Join(envPath, envName, c_cacheFileName), nil
}

func NewCacheManager(lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]) CacheManager {
	return &bicepEnvCache{
		lazyAzdContext: lazyAzdContext,
	}
}
