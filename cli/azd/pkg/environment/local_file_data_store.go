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
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/joho/godotenv"
	"golang.org/x/exp/slices"
)

type LocalFileDataStore struct {
	azdContext *azdcontext.AzdContext
}

func NewLocalFileDataStore(azdContext *azdcontext.AzdContext) LocalDataStore {
	return &LocalFileDataStore{
		azdContext: azdContext,
	}
}

// Path returns the path to the .env file for the given environment
func (fs *LocalFileDataStore) Path(env *Environment) string {
	return filepath.Join(fs.azdContext.EnvironmentRoot(env.name), DotEnvFileName)
}

// ConfigPath returns the path to the config.json file for the given environment
func (fs *LocalFileDataStore) ConfigPath(env *Environment) string {
	return filepath.Join(fs.azdContext.EnvironmentRoot(env.name), ConfigFileName)
}

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
				DotEnvPath: fs.azdContext.EnvironmentDotEnvPath(ent.Name()),
			}
			envs = append(envs, ev)
		}
	}

	slices.SortFunc(envs, func(a, b *contracts.EnvListEnvironment) bool {
		return a.Name < b.Name
	})

	return envs, nil
}

func (fs *LocalFileDataStore) Get(ctx context.Context, name string) (*Environment, error) {
	root := fs.azdContext.EnvironmentRoot(name)
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("'%s' %w, %w", name, ErrNotFound, err)
	}

	env := &Environment{
		name:       name,
		dotEnvPath: filepath.Join(fs.azdContext.EnvironmentRoot(name), DotEnvFileName),
		configPath: filepath.Join(fs.azdContext.EnvironmentRoot(name), ConfigFileName),
	}

	if err := fs.Reload(ctx, env); err != nil {
		return nil, err
	}

	return env, nil
}

// Reloads environment variables and configuration
func (fs *LocalFileDataStore) Reload(ctx context.Context, env *Environment) error {
	// Reload env values
	if envMap, err := godotenv.Read(fs.Path(env)); errors.Is(err, os.ErrNotExist) {
		env.dotenv = make(map[string]string)
		env.deletedKeys = make(map[string]struct{})
	} else if err != nil {
		return fmt.Errorf("loading .env: %w", err)
	} else {
		env.dotenv = envMap
		env.deletedKeys = make(map[string]struct{})
	}

	// Reload env config
	cfgMgr := config.NewManager()
	if cfg, err := cfgMgr.Load(fs.ConfigPath(env)); errors.Is(err, os.ErrNotExist) {
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

// If `Root` is set, Save writes the current contents of the environment to
// the given directory, creating it and any intermediate directories as needed.
func (fs *LocalFileDataStore) Save(ctx context.Context, env *Environment) error {
	// Update configuration
	cfgMgr := config.NewManager()
	if err := cfgMgr.Save(env.Config, fs.ConfigPath(env)); err != nil {
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

	root := fs.azdContext.EnvironmentRoot(env.name)
	err := os.MkdirAll(root, osutil.PermissionDirectory)
	if err != nil {
		return fmt.Errorf("failed to create a directory: %w", err)
	}

	// Instead of calling `godotenv.Write` directly, we need to save the file ourselves, so we can fixup any numeric values
	// that were incorrectly unquoted.
	marshalled, err := godotenv.Marshal(env.dotenv)
	if err != nil {
		return fmt.Errorf("saving .env: %w", err)
	}

	marshalled = fixupUnquotedDotenv(env.dotenv, marshalled)

	envFile, err := os.Create(fs.Path(env))
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

	tracing.SetUsageAttributes(fields.StringHashed(fields.EnvNameKey, env.GetEnvName()))
	return nil
}

func (fs *LocalFileDataStore) Create(ctx context.Context, name string) (*Environment, error) {
	dir := fs.azdContext.EnvironmentDirectory()
	if err := os.MkdirAll(dir, osutil.PermissionDirectory); err != nil {
		return nil, fmt.Errorf("creating environment root: %w", err)
	}

	if err := os.Mkdir(fs.azdContext.EnvironmentRoot(name), osutil.PermissionDirectory); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrExists
		}

		return nil, fmt.Errorf("creating environment directory: %w", err)
	}

	return fs.Get(ctx, name)
}

func (fs *LocalFileDataStore) Delete(ctx context.Context, name string) error {
	// TODO: Implement Delete function
	return nil
}

func (fs *LocalFileDataStore) Values(ctx context.Context) (map[string]string, error) {
	// TODO: Implement Values function
	return nil, nil
}

func (fs *LocalFileDataStore) Refresh(ctx context.Context) error {
	// TODO: Implement Refresh function
	return nil
}

func (fs *LocalFileDataStore) Select(name string) error {
	// TODO: Implement Select function
	return nil
}
