// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/gofrs/flock"
	"github.com/joho/godotenv"
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

// lockPath returns the path to the OS-level file lock used to serialize
// concurrent Reload/Save operations across processes (e.g. parallel
// `azd env set` subprocesses spawned from service hooks).
func (fs *LocalFileDataStore) lockPath(env Env) string {
	return filepath.Join(fs.azdContext.EnvironmentRoot(env.Name()), DotEnvFileName+".lock")
}

// newEnvLock returns an OS-level file lock on the .env file for `env`. The
// caller owns Lock()/Unlock(). The lock file itself is never deleted so
// concurrent holders can always discover it — flock semantics coordinate
// via the underlying inode, not via file presence.
func (fs *LocalFileDataStore) newEnvLock(env Env) (*flock.Flock, error) {
	if err := ValidateEnvironmentName(env.Name()); err != nil {
		return nil, err
	}
	path := fs.lockPath(env)
	if err := os.MkdirAll(filepath.Dir(path), osutil.PermissionDirectory); err != nil {
		return nil, fmt.Errorf("creating env dir for lock: %w", err)
	}
	return flock.New(path), nil
}

// envLockRetryDelay is the polling interval used by TryLockContext while
// waiting for another process to release the .env flock.
const envLockRetryDelay = 50 * time.Millisecond

// acquireEnvLock acquires the OS-level .env flock, polling so the caller's
// context (Ctrl-C, deadline) can cancel the wait. Without this, a wedged
// holder (e.g. a hung `azd env set` subprocess on Windows where LockFileEx
// blocks in a kernel wait that does not honor process signals) would
// freeze azd indefinitely.
func (fs *LocalFileDataStore) acquireEnvLock(ctx context.Context, env Env) (*flock.Flock, error) {
	fl, err := fs.newEnvLock(env)
	if err != nil {
		return nil, err
	}
	locked, err := fl.TryLockContext(ctx, envLockRetryDelay)
	if err != nil {
		return nil, fmt.Errorf("locking .env: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("locking .env: %w", ctx.Err())
	}
	return fl, nil
}

func releaseEnvLock(fl *flock.Flock) {
	if err := fl.Unlock(); err != nil {
		log.Printf("failed to release .env lock: %v", err)
	}
}

// Path returns the path to the .env file for the given environment
func (fs *LocalFileDataStore) EnvPath(env Env) string {
	return filepath.Join(fs.azdContext.EnvironmentRoot(env.Name()), DotEnvFileName)
}

// ConfigPath returns the path to the config.json file for the given environment
func (fs *LocalFileDataStore) ConfigPath(env Env) string {
	return filepath.Join(fs.azdContext.EnvironmentRoot(env.Name()), ConfigFileName)
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

	slices.SortFunc(envs, func(a, b *contracts.EnvListEnvironment) int {
		return strings.Compare(a.Name, b.Name)
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
func (fs *LocalFileDataStore) Reload(ctx context.Context, env Env) error {
	// Serialize against concurrent cross-process Save (e.g. parallel
	// `azd env set` subprocesses from service hooks) so we never observe
	// a truncated or partially-written .env file.
	fl, err := fs.acquireEnvLock(ctx, env)
	if err != nil {
		return err
	}
	defer releaseEnvLock(fl)

	values, err := readDotenv(fs.EnvPath(env))
	if err != nil {
		return err
	}
	cfg, err := fs.configManager.Load(fs.ConfigPath(env))
	if errors.Is(err, os.ErrNotExist) {
		cfg = config.NewEmptyConfig()
	} else if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	state := EnvironmentState{Dotenv: values, Config: cfg}
	if err := env.ReplaceState(state); err != nil {
		return err
	}
	traceLoadedState(env.Name(), state)
	return nil
}

func readDotenv(path string) (map[string]string, error) {
	values, err := godotenv.Read(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading .env: %w", err)
	}
	return values, nil
}

// Save saves the environment to the persistent data store
func (fs *LocalFileDataStore) Save(ctx context.Context, env Env, options *SaveOptions) error {
	// Acquire an OS-level file lock so the reload-merge-write cycle below is
	// atomic against other processes (parallel service hooks spawning
	// `azd env set` subprocesses, or another `azd` invocation). Without this
	// two concurrent writers would each read the current .env, merge their
	// own keys, then truncate-and-write — the last writer silently clobbers
	// the first writer's keys. The lock also covers the sibling config.json
	// write, which is similarly subject to torn reads on concurrent saves.
	//
	// Use WithoutCancel so that a canceled parent context (e.g. Ctrl-C during
	// first-run init) doesn't abort the save mid-flight — losing persisted
	// state is worse than a brief delay. The 30s timeout below bounds the wait
	// so we never hang indefinitely.
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer saveCancel()
	fl, err := fs.acquireEnvLock(saveCtx, env)
	if err != nil {
		return err
	}
	defer releaseEnvLock(fl)

	envPath, configPath := fs.EnvPath(env), fs.ConfigPath(env)
	storedDotenv, err := readDotenv(envPath)
	if err != nil {
		return fmt.Errorf("failed reloading env vars, %w", err)
	}
	var savedState EnvironmentState
	if err := env.MergeAndSave(saveCtx, storedDotenv, func(ctx context.Context, state EnvironmentState) error {
		if err := fs.configManager.Save(state.Config, configPath); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}
		marshalled, err := marshallDotEnv(state.Dotenv)
		if err != nil {
			return err
		}
		if err := writeDotenv(ctx, envPath, marshalled); err != nil {
			return err
		}
		savedState = state
		return nil
	}); err != nil {
		return err
	}
	traceLoadedState(env.Name(), savedState)
	return nil
}

// writeDotenv writes through a sibling file and atomic rename. Caller holds the file lock.
func writeDotenv(ctx context.Context, envPath string, marshalled string) error {
	// Best-effort sweep of stale tmp files (>1h old) left behind by prior
	// crashed/SIGKILL'd writers. Safe under the flock — no concurrent
	// in-flight tmp files possible.
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(envPath), DotEnvFileName+".tmp-*")); len(matches) > 0 {
		for _, m := range matches {
			if info, statErr := os.Stat(m); statErr == nil && time.Since(info.ModTime()) > time.Hour {
				_ = os.Remove(m)
			}
		}
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(envPath), DotEnvFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp .env: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmpFile.WriteString(marshalled + "\n"); err != nil {
		return fmt.Errorf("writing temp .env: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("syncing temp .env: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp .env: %w", err)
	}
	if err := osutil.Rename(ctx, tmpPath, envPath); err != nil {
		return fmt.Errorf("renaming temp .env: %w", err)
	}

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
