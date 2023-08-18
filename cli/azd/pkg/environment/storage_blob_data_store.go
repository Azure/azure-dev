package environment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/joho/godotenv"
	"golang.org/x/exp/slices"
)

type StorageBlobDataStore struct {
	blobClient storage.BlobClient
}

func NewStorageBlobDataStore(blobClient storage.BlobClient) DataStore {
	return &StorageBlobDataStore{
		blobClient: blobClient,
	}
}

// Path returns the path to the .env file for the given environment
func (fs *StorageBlobDataStore) Path(env *Environment) string {
	return fmt.Sprintf("%s/%s", env.name, DotEnvFileName)
}

// ConfigPath returns the path to the config.json file for the given environment
func (fs *StorageBlobDataStore) ConfigPath(env *Environment) string {
	return fmt.Sprintf("%s/%s", env.name, ConfigFileName)
}

func (sbd *StorageBlobDataStore) List(ctx context.Context) ([]*contracts.EnvListEnvironment, error) {
	blobs, err := sbd.blobClient.Items(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrContainerNotFound) {
			return []*contracts.EnvListEnvironment{}, nil
		}

		return nil, fmt.Errorf("listing blobs: %w", err)
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
		return nil, fmt.Errorf("%s %w", name, ErrNotFound)
	}

	matchingEnv := envs[matchingIndex]
	env := &Environment{
		name:       matchingEnv.Name,
		dotEnvPath: matchingEnv.DotEnvPath,
		configPath: matchingEnv.ConfigPath,
	}

	if err := sbd.Reload(ctx, env); err != nil {
		return nil, err
	}

	return env, nil
}

func (sbd *StorageBlobDataStore) Save(ctx context.Context, env *Environment) error {
	// Update configuration
	cfgWriter := new(bytes.Buffer)
	cfgMgr := config.NewManager()

	if err := cfgMgr.Save(env.Config, cfgWriter); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	if err := sbd.blobClient.Upload(ctx, sbd.ConfigPath(env), cfgWriter); err != nil {
		return fmt.Errorf("uploading config: %w", err)
	}

	// Instead of calling `godotenv.Write` directly, we need to save the file ourselves, so we can fixup any numeric values
	// that were incorrectly unquoted.
	marshalled, err := godotenv.Marshal(env.dotenv)
	if err != nil {
		return fmt.Errorf("saving .env: %w", err)
	}

	marshalled = fixupUnquotedDotenv(env.dotenv, marshalled)
	marshalled += "\n"

	buffer := bytes.NewBuffer([]byte(marshalled))

	if err := sbd.blobClient.Upload(ctx, sbd.Path(env), buffer); err != nil {
		return fmt.Errorf("uploading .env: %w", err)
	}

	tracing.SetUsageAttributes(fields.StringHashed(fields.EnvNameKey, env.GetEnvName()))
	return nil
}

func (sbd *StorageBlobDataStore) Reload(ctx context.Context, env *Environment) error {
	// Reload .env file
	dotEnvBuffer, err := sbd.blobClient.Download(ctx, sbd.Path(env))
	if err != nil {
		return err
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
		return err
	}

	defer configBuffer.Close()

	cfgMgr := config.NewManager()
	if cfg, err := cfgMgr.Load(configBuffer); errors.Is(err, os.ErrNotExist) {
		env.Config = config.NewEmptyConfig()
	} else if err != nil {
		return fmt.Errorf("loading config: %w", err)
	} else {
		env.Config = cfg
	}

	if env.GetEnvName() != "" {
		tracing.SetUsageAttributes(fields.StringHashed(fields.EnvNameKey, env.GetEnvName()))
	}

	if env.GetSubscriptionId() != "" {
		tracing.SetGlobalAttributes(fields.SubscriptionIdKey.String(env.GetSubscriptionId()))
	}

	return nil
}
