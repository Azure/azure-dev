// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

var (
	ErrAccessDenied     = errors.New("access denied connecting Azure Blob Storage container.")
	ErrInvalidContainer = errors.New("storage container name is invalid.")

	// ErrRemoteStateConflict is returned when remote environment blobs were
	// changed by another process between load and save.
	ErrRemoteStateConflict = errors.New(
		"the remote environment was modified by another process")
)

type StorageBlobDataStore struct {
	configManager config.Manager
	blobClient    storage.BlobClient

	// blobETags maps blob path to the ETag from the last load. A nil value
	// means the blob did not exist (If-None-Match: *). Missing keys are
	// resolved with a download before save.
	etagMu    sync.Mutex
	blobETags map[string]*azcore.ETag
}

func NewStorageBlobDataStore(configManager config.Manager, blobClient storage.BlobClient) RemoteDataStore {
	return &StorageBlobDataStore{
		configManager: configManager,
		blobClient:    blobClient,
		blobETags:     map[string]*azcore.ETag{},
	}
}

// EnvPath returns the path to the .env file for the given environment
func (fs *StorageBlobDataStore) EnvPath(env *Environment) (string, error) {
	if !IsValidEnvironmentName(env.Name()) {
		return "", InvalidEnvironmentNameError(env.Name())
	}
	return fmt.Sprintf("%s/%s", env.name, DotEnvFileName), nil
}

// ConfigPath returns the path to the config.json file for the given environment
func (fs *StorageBlobDataStore) ConfigPath(env *Environment) (string, error) {
	if !IsValidEnvironmentName(env.Name()) {
		return "", InvalidEnvironmentNameError(env.Name())
	}
	return fmt.Sprintf("%s/%s", env.name, ConfigFileName), nil
}

func (sbd *StorageBlobDataStore) List(ctx context.Context) ([]*contracts.EnvListEnvironment, error) {
	blobs, err := sbd.blobClient.Items(ctx)
	if err != nil {
		normalizedErr := describeError(err)

		if errors.Is(normalizedErr, storage.ErrContainerNotFound) {
			return []*contracts.EnvListEnvironment{}, nil
		}

		return nil, fmt.Errorf("listing blobs: %w", normalizedErr)
	}

	envMap := map[string]*contracts.EnvListEnvironment{}

	for _, blob := range blobs {
		// Blob keys use forward slashes on every platform. Only direct state files
		// match the keys used by Save and Reload.
		envName, fileName, found := strings.Cut(blob.Path, "/")
		if !found || !IsValidEnvironmentName(envName) ||
			(fileName != DotEnvFileName && fileName != ConfigFileName) {
			log.Printf("skipping environment blob %q: invalid state layout", blob.Path)
			continue
		}
		env, has := envMap[envName]
		if !has {
			env = &contracts.EnvListEnvironment{
				Name: envName,
			}
			envMap[envName] = env
		}

		switch fileName {
		case ConfigFileName:
			env.ConfigPath = blob.Path
		case DotEnvFileName:
			env.DotEnvPath = blob.Path
		}
	}

	envs := []*contracts.EnvListEnvironment{}
	for _, env := range envMap {
		envs = append(envs, env)
	}

	slices.SortFunc(envs, func(a, b *contracts.EnvListEnvironment) int {
		return strings.Compare(a.Name, b.Name)
	})

	return envs, nil
}

func (sbd *StorageBlobDataStore) Get(ctx context.Context, name string) (*Environment, error) {
	envs, err := sbd.List(ctx)
	if err != nil {
		return nil, err
	}

	matchingIndex := slices.IndexFunc(envs, func(env *contracts.EnvListEnvironment) bool {
		return env.Name == name
	})

	if matchingIndex < 0 {
		return nil, fmt.Errorf("'%s': %w", name, ErrNotFound)
	}

	matchingEnv := envs[matchingIndex]
	env := &Environment{
		name: matchingEnv.Name,
	}

	if err := sbd.Reload(ctx, env); err != nil {
		return nil, err
	}

	return env, nil
}

func (sbd *StorageBlobDataStore) Save(ctx context.Context, env *Environment, options *SaveOptions) error {
	configPath, err := sbd.ConfigPath(env)
	if err != nil {
		return err
	}
	envPath, err := sbd.EnvPath(env)
	if err != nil {
		return err
	}

	// Update configuration
	cfgWriter := new(bytes.Buffer)

	if err := sbd.configManager.Save(env.Config, cfgWriter); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	if err := sbd.uploadBlob(ctx, configPath, cfgWriter); err != nil {
		return fmt.Errorf("uploading config: %w", describeError(err))
	}

	marshalled, err := marshallDotEnv(env)
	if err != nil {
		return fmt.Errorf("marshalling .env: %w", err)
	}

	buffer := bytes.NewBuffer([]byte(marshalled))

	if err := sbd.uploadBlob(ctx, envPath, buffer); err != nil {
		return fmt.Errorf("uploading .env: %w", describeError(err))
	}

	tracing.SetUsageAttributes(fields.StringHashed(fields.EnvNameKey, env.Name()))
	return nil
}

func (sbd *StorageBlobDataStore) Reload(ctx context.Context, env *Environment) error {
	envPath, err := sbd.EnvPath(env)
	if err != nil {
		return err
	}
	configPath, err := sbd.ConfigPath(env)
	if err != nil {
		return err
	}

	// Reload .env file
	dotEnvDownload, err := sbd.blobClient.Download(ctx, envPath)
	if err != nil {
		return describeError(err)
	}

	defer dotEnvDownload.Body.Close()
	sbd.setBlobETag(envPath, new(dotEnvDownload.ETag))

	envMap, err := godotenv.Parse(dotEnvDownload.Body)
	if err != nil {
		return fmt.Errorf("loading .env: %w", err)
	}

	// Reload config file
	configDownload, err := sbd.blobClient.Download(ctx, configPath)
	if err != nil {
		return describeError(err)
	}

	defer configDownload.Body.Close()
	sbd.setBlobETag(configPath, new(configDownload.ETag))

	cfg, err := sbd.configManager.Load(configDownload.Body)
	if errors.Is(err, os.ErrNotExist) {
		cfg = config.NewEmptyConfig()
	} else if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	env.replaceState(envMap, make(map[string]struct{}))
	env.Config = cfg

	if env.Name() != "" {
		tracing.SetUsageAttributes(fields.StringHashed(fields.EnvNameKey, env.Name()))
	}

	if _, err := uuid.Parse(env.GetSubscriptionId()); err == nil {
		tracing.SetGlobalAttributes(fields.SubscriptionIdKey.String(env.GetSubscriptionId()))
	} else {
		tracing.SetGlobalAttributes(fields.StringHashed(fields.SubscriptionIdKey, env.GetSubscriptionId()))
	}

	return nil
}

func (sbd *StorageBlobDataStore) Delete(ctx context.Context, name string) error {
	envs, err := sbd.List(ctx)
	if err != nil {
		return describeError(err)
	}

	matchingIndex := slices.IndexFunc(envs, func(env *contracts.EnvListEnvironment) bool {
		return env.Name == name
	})

	if matchingIndex < 0 {
		return fmt.Errorf("'%s': %w", name, ErrNotFound)
	}

	env := envs[matchingIndex]
	if env.ConfigPath != "" {
		err := sbd.blobClient.Delete(ctx, env.ConfigPath)
		if err != nil {
			return fmt.Errorf("deleting remote config: %w", describeError(err))
		}
	}

	if env.DotEnvPath != "" {
		err := sbd.blobClient.Delete(ctx, env.DotEnvPath)
		if err != nil {
			return fmt.Errorf("deleting remote .env: %w", describeError(err))
		}
	}

	return nil
}

func describeError(err error) error {
	if _, ok := errors.AsType[*storage.ConcurrencyConflictError](err); ok {
		return &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("%w: %w", ErrRemoteStateConflict, err),
			Message: "The remote environment was modified by another process " +
				"and this save was not applied.",
			Suggestion: "Refresh the environment from remote state and retry the operation.",
			Links:      []errorhandler.ErrorLink{},
		}
	}

	if responseErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		switch responseErr.ErrorCode {
		case "AuthorizationPermissionMismatch":
			errorMsg := "Ensure your Azure account has `Storage Blob Contributor` role on the storage account or container."
			return fmt.Errorf("%w %s %w", ErrAccessDenied, errorMsg, err)
		case "InvalidResourceName":
			//nolint:lll
			errorMsg := "It must be between 3 and 63 characters in length, and must contain only lowercase letters, numbers, and dashes."
			return fmt.Errorf("%w %s %w", ErrInvalidContainer, errorMsg, err)
		}
	}

	return err
}

func (sbd *StorageBlobDataStore) uploadBlob(ctx context.Context, blobPath string, reader *bytes.Buffer) error {
	match, err := sbd.matchETag(ctx, blobPath)
	if err != nil {
		return err
	}

	etag, err := sbd.blobClient.Upload(ctx, blobPath, reader, match)
	if err != nil {
		return err
	}

	sbd.setBlobETag(blobPath, new(etag))
	return nil
}

func (sbd *StorageBlobDataStore) matchETag(ctx context.Context, blobPath string) (azcore.ETag, error) {
	sbd.etagMu.Lock()
	if etag, ok := sbd.blobETags[blobPath]; ok {
		sbd.etagMu.Unlock()
		if etag == nil {
			return "", nil
		}
		return *etag, nil
	}
	sbd.etagMu.Unlock()

	download, err := sbd.blobClient.Download(ctx, blobPath)
	if isBlobNotFound(err) {
		sbd.setBlobETag(blobPath, nil)
		return "", nil
	}
	if err != nil {
		return "", err
	}

	defer download.Body.Close()
	sbd.setBlobETag(blobPath, new(download.ETag))
	return download.ETag, nil
}

func (sbd *StorageBlobDataStore) setBlobETag(blobPath string, etag *azcore.ETag) {
	sbd.etagMu.Lock()
	defer sbd.etagMu.Unlock()
	sbd.blobETags[blobPath] = etag
}

func isBlobNotFound(err error) bool {
	respErr, ok := errors.AsType[*azcore.ResponseError](err)
	if !ok {
		return false
	}
	return respErr.ErrorCode == "BlobNotFound" || respErr.StatusCode == http.StatusNotFound
}
