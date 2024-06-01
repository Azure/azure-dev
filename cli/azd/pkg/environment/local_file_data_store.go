package environment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"golang.org/x/exp/slices"
)

// LocalFileDataStore is a DataStore implementation that stores environment data in the local file system.
type LocalFileDataStore struct {
	azdContext    *azdcontext.AzdContext
	configManager config.FileConfigManager
}

// NewLocalFileDataStore creates a new LocalFileDataStore instance
func NewLocalFileDataStore(azdContext *azdcontext.AzdContext, configManager config.FileConfigManager) LocalDataStore {
	return &LocalFileDataStore{
		azdContext:    azdContext,
		configManager: configManager,
	}
}

// Path returns the path to the .env file for the given environment
func (fs *LocalFileDataStore) EnvPath(env *Environment) string {
	return filepath.Join(fs.azdContext.EnvironmentRoot(env.name), DotEnvFileName)
}

// ConfigPath returns the path to the config.json file for the given environment
func (fs *LocalFileDataStore) ConfigPath(env *Environment) string {
	return filepath.Join(fs.azdContext.EnvironmentRoot(env.name), ConfigFileName)
}

// List returns a list of all environments within the data store
func (fs *LocalFileDataStore) List(ctx context.Context) ([]*contracts.EnvListEnvironment, error) {
	defaultEnv, err := fs.azdContext.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}

	environments, err := os.ReadDir(fs.azdContext.EnvironmentDirectory())
	if errors.Is(err, os.ErrNotExist) {
		return []*contracts.EnvListEnvironment{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing entries: %w", err)
	}

	// prefer empty array over `nil` since this is a contracted return value,
	// where empty array is preferred for "NotFound" semantics.
	envs := []*contracts.EnvListEnvironment{}
	for _, ent := range environments {
		if ent.IsDir() {
			ev := &contracts.EnvListEnvironment{
				Name:       ent.Name(),
				IsDefault:  ent.Name() == defaultEnv,
				DotEnvPath: filepath.Join(fs.azdContext.EnvironmentRoot(ent.Name()), DotEnvFileName),
				ConfigPath: filepath.Join(fs.azdContext.EnvironmentRoot(ent.Name()), ConfigFileName),
			}
			envs = append(envs, ev)
		}
	}

	slices.SortFunc(envs, func(a, b *contracts.EnvListEnvironment) bool {
		return a.Name < b.Name
	})

	return envs, nil
}

// Get returns the environment instance for the specified environment name
func (fs *LocalFileDataStore) Get(ctx context.Context, name string) (*Environment, error) {
	root := fs.azdContext.EnvironmentRoot(name)
	_, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("'%s': %w", name, ErrNotFound)
	} else if err != nil {
		return nil, fmt.Errorf("listing env root: %w", err)
	}

	env := New(name)
	if err := fs.Reload(ctx, env); err != nil {
		return nil, err
	}

	return env, nil
}

// Reload reloads the environment from the persistent data store
func (fs *LocalFileDataStore) Reload(ctx context.Context, env *Environment) error {
	// Reload env values
	if envMap, err := godotenv.Read(fs.EnvPath(env)); errors.Is(err, os.ErrNotExist) {
		env.dotenv = make(map[string]string)
		env.deletedKeys = make(map[string]struct{})
	} else if err != nil {
		return fmt.Errorf("loading .env: %w", err)
	} else {
		env.dotenv = envMap
		env.deletedKeys = make(map[string]struct{})
	}

	// Reload env config
	if cfg, err := fs.configManager.Load(fs.ConfigPath(env)); errors.Is(err, os.ErrNotExist) {
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

// Save saves the environment to the persistent data store
func (fs *LocalFileDataStore) Save(ctx context.Context, env *Environment, options *SaveOptions) error {
	// Update configuration
	if err := fs.configManager.Save(env.Config, fs.ConfigPath(env)); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	// Cache current values & reload to get any new env vars
	currentValues := env.dotenv
	deletedValues := env.deletedKeys
	if err := fs.Reload(ctx, env); err != nil {
		return fmt.Errorf("failed reloading env vars, %w", err)
	}

	// Overlay current values before saving
	for key, value := range currentValues {
		env.dotenv[key] = value
	}

	// Replay deletion
	for key := range deletedValues {
		delete(env.dotenv, key)
	}

	marshalled, err := marshallDotEnv(env)
	if err != nil {
		return fmt.Errorf("marshalling .env: %w", err)
	}

	envFile, err := os.Create(fs.EnvPath(env))
	if err != nil {
		return fmt.Errorf("saving .env: %w", err)
	}
	defer envFile.Close()

	// Write the contents (with a trailing newline), and sync the file, as godotenv.Write would have.
	if _, err := envFile.WriteString(marshalled + "\n"); err != nil {
		return fmt.Errorf("saving .env: %w", err)
	}

	if err := envFile.Sync(); err != nil {
		return fmt.Errorf("saving .env: %w", err)
	}

	tracing.SetUsageAttributes(fields.StringHashed(fields.EnvNameKey, env.Name()))
	return nil
}

func (fs *LocalFileDataStore) Delete(ctx context.Context, name string) error {
	envRoot := fs.azdContext.EnvironmentRoot(name)
	_, err := os.Stat(envRoot)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("'%s': %w", name, ErrNotFound)
	} else if err != nil {
		return fmt.Errorf("listing env root: %w", err)
	}

	if err := os.RemoveAll(envRoot); err != nil {
		return fmt.Errorf("removing env root: %w", err)
	}

	return nil
}
