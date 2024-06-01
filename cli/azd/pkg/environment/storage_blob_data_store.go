package environment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"golang.org/x/exp/slices"
)

var (
	ErrAccessDenied     = errors.New("access denied connecting Azure Blob Storage container.")
	ErrInvalidContainer = errors.New("storage container name is invalid.")
)

type StorageBlobDataStore struct {
	configManager config.Manager
	blobClient    storage.BlobClient
}

func NewStorageBlobDataStore(configManager config.Manager, blobClient storage.BlobClient) RemoteDataStore {
	return &StorageBlobDataStore{
		configManager: configManager,
		blobClient:    blobClient,
	}
}

// EnvPath returns the path to the .env file for the given environment
func (fs *StorageBlobDataStore) EnvPath(env *Environment) string {
	return fmt.Sprintf("%s/%s", env.name, DotEnvFileName)
}

// ConfigPath returns the path to the config.json file for the given environment
func (fs *StorageBlobDataStore) ConfigPath(env *Environment) string {
	return fmt.Sprintf("%s/%s", env.name, ConfigFileName)
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
		envName := filepath.Base(filepath.Dir(blob.Path))
		env, has := envMap[envName]
		if !has {
			env = &contracts.EnvListEnvironment{
				Name: envName,
			}
			envMap[envName] = env
		}

		switch blob.Name {
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

	slices.SortFunc(envs, func(a, b *contracts.EnvListEnvironment) bool {
		return a.Name < b.Name
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
	// Update configuration
	cfgWriter := new(bytes.Buffer)

	if err := sbd.configManager.Save(env.Config, cfgWriter); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	if err := sbd.blobClient.Upload(ctx, sbd.ConfigPath(env), cfgWriter); err != nil {
		return fmt.Errorf("uploading config: %w", describeError(err))
	}

	marshalled, err := marshallDotEnv(env)
	if err != nil {
		return fmt.Errorf("marshalling .env: %w", err)
	}

	buffer := bytes.NewBuffer([]byte(marshalled))

	if err := sbd.blobClient.Upload(ctx, sbd.EnvPath(env), buffer); err != nil {
		return fmt.Errorf("uploading .env: %w", describeError(err))
	}

	tracing.SetUsageAttributes(fields.StringHashed(fields.EnvNameKey, env.Name()))
	return nil
}

func (sbd *StorageBlobDataStore) Reload(ctx context.Context, env *Environment) error {
	// Reload .env file
	dotEnvBuffer, err := sbd.blobClient.Download(ctx, sbd.EnvPath(env))
	if err != nil {
		return describeError(err)
	}

	defer dotEnvBuffer.Close()

	envMap, err := godotenv.Parse(dotEnvBuffer)
	if err != nil {
		env.dotenv = make(map[string]string)
		env.deletedKeys = make(map[string]struct{})
	} else {
		env.dotenv = envMap
		env.deletedKeys = make(map[string]struct{})
	}

	// Reload config file
	configBuffer, err := sbd.blobClient.Download(ctx, sbd.ConfigPath(env))
	if err != nil {
		return describeError(err)
	}

	defer configBuffer.Close()

	if cfg, err := sbd.configManager.Load(configBuffer); errors.Is(err, os.ErrNotExist) {
		env.Config = config.NewEmptyConfig()
	} else if err != nil {
		return fmt.Errorf("loading config: %w", err)
	} else {
		env.Config = cfg
	}

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
	var responseErr *azcore.ResponseError

	if errors.As(err, &responseErr) {
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
