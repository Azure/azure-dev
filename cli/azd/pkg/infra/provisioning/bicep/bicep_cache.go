// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"golang.org/x/exp/slices"
)

// BicepCache is serialized into json and represents the local provisioning cache for bicep.
type BicepCache struct {
	Template   json.RawMessage
	Parameters azure.ArmParameters
}

// CacheManager provides the functionality for using bicep cache.
type CacheManager interface {
	// Current returns existing cache or nil when there's no cache.
	Current(context context.Context) *BicepCache
	// Cache persist cache. Use Cache(nil) to clear cache.
	Cache(context context.Context, cache *BicepCache) error
	// Equal compares cache against current and return false when they are different.
	Equal(context context.Context, cache *BicepCache) bool
}

// bicepCache writes a cache file inside the current azd environment.
// Remote cache is also supported by setting the env var:
//
// AZURE_BICEP_CACHE_CONFIG=type,args...
//
// where supported `type` can be:
// - azureBlob
//
// `args` depends on the type as follows:
//
// **azureBlob args: `containerUrl,sas`,
// example:
//
//	   AZURE_BICEP_CACHE_CONFIG=azureBlob,https://<storagename>.blob.core.windows.net/<containername>,sv=2019-12-12&ss...
//
//	The Shared Access Signature (sas) enables the authentication to the Azure Storage Container
//
// If the manager fails to parse or connect to the remote config, it will fallback automatically to use local file cache.
type bicepCache struct {
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]
	lazyAzdEnv     *lazy.Lazy[*environment.Environment]
	// for testing purposes, allows to mock the implementation for reading/writing cache.
	overrideReadFunc  readCache
	overrideWriteFunc writeCache
}

const c_cacheFileName = "bicep.cache"

type readCache func(context context.Context, arg any) ([]byte, error)
type writeCache func(context context.Context, arg any, cache []byte) error

func cacheFromAzureBlob(context context.Context, arg any) ([]byte, error) {
	return nil, nil
}

func cacheFromLocalFile(context context.Context, arg any) ([]byte, error) {
	localFilePath, goodCast := arg.(string)
	if !goodCast {
		return nil, fmt.Errorf("unexpected data type for read cache from file implementation")
	}
	_, err := os.Stat(localFilePath)
	if err != nil {
		log.Printf("couldn't get info about cache file: %v. Ignoring cache.", err)
		return nil, nil
	}
	fileContent, err := os.ReadFile(localFilePath)
	if err != nil {
		log.Printf("couldn't read cache file: %v. Ignoring cache.", err)
		return nil, nil
	}
	return fileContent, nil
}

func cacheToAzureBlob(context context.Context, arg any, cache []byte) error {
	return nil
}

func cacheToLocalFile(context context.Context, arg any, cache []byte) error {
	localFilePath, goodCast := arg.(string)
	if !goodCast {
		return fmt.Errorf("unexpected data type for write cache to file implementation")
	}
	_ = os.Remove(localFilePath)
	err := os.WriteFile(localFilePath, cache, osutil.PermissionFile)
	if err != nil {
		log.Printf("unable to write cache to file: %v", err)
		return fmt.Errorf("saving bicep cache %w", err)
	}
	log.Printf("Cache saved to file.")
	return nil
}

func (b *bicepCache) checkReadOverride(impl readCache) readCache {
	if b.overrideReadFunc != nil {
		return b.overrideReadFunc
	}
	return impl
}

func (b *bicepCache) checkWriteOverride(impl writeCache) writeCache {
	if b.overrideWriteFunc != nil {
		return b.overrideWriteFunc
	}
	return impl
}

func (b *bicepCache) Current(context context.Context) *BicepCache {
	cacheSources, err := b.cacheSources()
	if err != nil {
		log.Printf("couldn't get cache sources: %v. Ignoring cache.", err)
		return nil
	}

	var cacheContent []byte

	if cacheSources.AzBlob != nil {
		readImplementation := b.checkReadOverride(cacheFromAzureBlob)
		azureBlobContent, err := readImplementation(context, cacheSources.AzBlob)
		if err != nil {
			log.Printf("error getting cache from Azure Storage: %v. Will fall back to local file.", err)
		} else {
			log.Printf("pulled cache from Azure Storage successfully")
			cacheContent = azureBlobContent
		}
	}

	if cacheContent == nil {
		readImplementation := b.checkReadOverride(cacheFromLocalFile)
		fileContent, err := readImplementation(context, cacheSources.localFilePath)
		if err != nil {
			log.Printf("error getting cache from local file: %v. Will ignore cache.", err)
			return nil
		} else {
			log.Printf("pulled cache from local file successfully")
			cacheContent = fileContent
		}
	}

	var cache BicepCache
	if err := json.Unmarshal(cacheContent, &cache); err != nil {
		log.Printf("couldn't parse cache: %v. Ignoring cache.", err)
		return nil
	}

	return &cache
}

func (b *bicepCache) Cache(context context.Context, cache *BicepCache) error {
	cacheSources, err := b.cacheSources()
	if err != nil {
		log.Printf("couldn't get cache sources: %v", err)
		return err
	}

	cacheContent, err := json.Marshal(cache)
	if err != nil {
		log.Printf("unable to marshall new cache: %v", err)
		return fmt.Errorf("saving bicep cache %w", err)
	}

	if cacheSources.AzBlob != nil {
		writeImplementation := b.checkWriteOverride(cacheToAzureBlob)
		err := writeImplementation(context, cacheSources.AzBlob, cacheContent)
		if err != nil {
			log.Printf("error pushing cache to Azure Storage: %v. Will fall back to local file.", err)
		} else {
			log.Printf("pushed cache to Azure Storage successfully")
			return nil
		}
	}

	writeImplementation := b.checkWriteOverride(cacheToLocalFile)
	err = writeImplementation(context, cacheSources.localFilePath, cacheContent)
	if err != nil {
		log.Printf("error saving cache to local file: %v. Will ignore cache.", err)
	} else {
		log.Printf("saved cache to local file successfully")
	}
	return nil
}

func (b *bicepCache) Equal(context context.Context, cache *BicepCache) bool {
	// cache is saved w/o format, hence, the comparison needs to be the same way
	currentCache, _ := json.Marshal(b.Current(context))
	rawCache, _ := json.Marshal(cache)
	cacheIsEqual := slices.Equal(currentCache, rawCache)
	log.Printf("Comparing cache - result: %t", cacheIsEqual)
	return slices.Equal(currentCache, rawCache)
}

const c_cacheTypeAzContainer = "azureBlob"

/* #nosec G101 - Potential hardcoded credentials - false positive */
const c_cacheRemoteConfigEnvName = "AZURE_BICEP_CACHE_CONFIG"

type azBlobSource struct {
	azContainerUrl string
	azContainerSas string
}

type cacheSources struct {
	localFilePath string
	AzBlob        *azBlobSource
}

// cacheSources finds all the available sources for bicep cache.
// Local file source is always added
func (b *bicepCache) cacheSources() (*cacheSources, error) {
	azdcontext, err := b.lazyAzdContext.GetValue()
	if err != nil {
		log.Printf("could't get azd context: %v", err)
		return nil, err
	}
	azdEnv, err := b.lazyAzdEnv.GetValue()
	if err != nil {
		log.Printf("could't get azd env: %v", err)
		return nil, err
	}

	availableSources := &cacheSources{}

	// Check if there is remote config to use
	if remoteConfig := azdEnv.Getenv(c_cacheRemoteConfigEnvName); remoteConfig != "" {
		remoteType, args, err := parseRemoteConfig(remoteConfig)
		if err != nil {
			log.Printf("error trying to apply remote cache source: %v. Source will be ignored.", err)
		} else {
			if remoteType == c_cacheTypeAzContainer {
				if azBlobConfig, err := parseAzContainerArgs(args); err != nil {
					availableSources.AzBlob = azBlobConfig
					log.Printf("Remote type: %s added to sources.", remoteType)
				} else {
					log.Printf("Couldn't add remote cache: %s. error: %v.", remoteType, err)
				}
			} else {
				log.Printf("Unsupported remote cache: %s.", remoteType)
			}
		}
	}

	envPath := azdcontext.EnvironmentDirectory()
	envName, err := azdcontext.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}
	cachePath := filepath.Join(envPath, envName, c_cacheFileName)
	availableSources.localFilePath = cachePath
	log.Printf("Added local cache source: %s", cachePath)
	return availableSources, nil
}

func parseRemoteConfig(config string) (string, []string, error) {
	configTokens := strings.Split(config, ",")
	if len(configTokens) <= 1 {
		return "", nil, fmt.Errorf(
			"expected more than one token separated by commas, but found: '%s'", config)
	}
	return configTokens[0], configTokens[1:], nil
}

func parseAzContainerArgs(args []string) (*azBlobSource, error) {
	argsCount := len(args)
	if argsCount <= 1 {
		return nil, fmt.Errorf("Azure blob requires 'url,sas'")
	}
	if argsCount > 2 {
		log.Printf("more than two arguments found for Azure blob remote config. Extra arguments will be ignored.")
	}
	return &azBlobSource{
		azContainerUrl: args[0],
		azContainerSas: args[1],
	}, nil
}

func NewCacheManager(lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]) CacheManager {
	return &bicepCache{
		lazyAzdContext: lazyAzdContext,
	}
}
