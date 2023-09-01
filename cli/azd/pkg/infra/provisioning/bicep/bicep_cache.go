// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"golang.org/x/exp/slices"
)

// cache is serialized into json and represents the local provisioning cache for bicep.
type cache struct {
	Template   json.RawMessage
	Parameters azure.ArmParameters
}

// CacheManager provides the functionality for using bicep cache.
type CacheManager interface {
	// Current returns existing cache or nil when there's no cache.
	Current(context context.Context) *cache
	// Cache persist cache. Use Cache(nil) to clear cache.
	Cache(context context.Context, cache *cache) error
	// Equal compares cache against current and return false when they are different.
	Equal(context context.Context, cache *cache) bool
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
// **azureBlob args: `containerName,connectionString`,
// example:
//
//	AZURE_BICEP_CACHE_CONFIG=azureBlob,cache,DefaultEndpointsProtocol=https;Ac....
//
// If the manager fails to parse or connect to the remote config, it will fallback automatically to use local file cache.
type cacheClient struct {
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
	azBlobData, goodCast := arg.(*azBlobSource)
	if !goodCast {
		return nil, fmt.Errorf("unexpected data for pulling cache from Azure Blob.")
	}
	client, err := azblob.NewClientFromConnectionString(azBlobData.azStorageConnectionString, nil)
	if err != nil {
		return nil, fmt.Errorf("creating client from connection string: %w", err)
	}

	blobDownloadResponse, err := client.DownloadStream(context, azBlobData.azContainerName, c_cacheFileName, nil)
	if err != nil {
		return nil, fmt.Errorf("downloading blob from azure: %w", err)
	}
	return io.ReadAll(blobDownloadResponse.Body)
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
	azBlobData, goodCast := arg.(*azBlobSource)
	if !goodCast {
		return fmt.Errorf("unexpected data for pushing cache to Azure Blob.")
	}
	client, err := azblob.NewClientFromConnectionString(azBlobData.azStorageConnectionString, nil)
	if err != nil {
		return fmt.Errorf("creating client from connection string: %w", err)
	}
	// Blob might not exists, it's ok if there's an error.
	_, _ = client.DeleteBlob(context, azBlobData.azContainerName, c_cacheFileName, nil)
	// Container might already exists, it's fine to ignore error.
	_, _ = client.CreateContainer(context, azBlobData.azContainerName, nil)
	_, err = client.UploadBuffer(context, azBlobData.azContainerName, c_cacheFileName, cache, &azblob.UploadBufferOptions{})
	if err != nil {
		return fmt.Errorf("uploading blob from azure: %w", err)
	}
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

func (b *cacheClient) checkReadOverride(impl readCache) readCache {
	if b.overrideReadFunc != nil {
		return b.overrideReadFunc
	}
	return impl
}

func (b *cacheClient) checkWriteOverride(impl writeCache) writeCache {
	if b.overrideWriteFunc != nil {
		return b.overrideWriteFunc
	}
	return impl
}

func (b *cacheClient) Current(context context.Context) *cache {
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

	var cache cache
	if err := json.Unmarshal(cacheContent, &cache); err != nil {
		log.Printf("couldn't parse cache: %v. Ignoring cache.", err)
		return nil
	}

	return &cache
}

func (b *cacheClient) Cache(context context.Context, cache *cache) error {
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

func (b *cacheClient) Equal(context context.Context, cache *cache) bool {
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
	azContainerName           string
	azStorageConnectionString string
}

type cacheSources struct {
	localFilePath string
	AzBlob        *azBlobSource
}

// cacheSources finds all the available sources for bicep cache.
// Local file source is always added
func (b *cacheClient) cacheSources() (*cacheSources, error) {
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
					log.Printf("Couldn't add remote cache: %s. error: %v.", remoteType, err)
				} else {
					availableSources.AzBlob = azBlobConfig
					log.Printf("Remote type: %s added to sources.", remoteType)
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
		azContainerName:           args[0],
		azStorageConnectionString: args[1],
	}, nil
}

func NewCacheManager(
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnv *lazy.Lazy[*environment.Environment]) CacheManager {
	return &cacheClient{
		lazyAzdContext: lazyAzdContext,
		lazyAzdEnv:     lazyEnv,
	}
}
