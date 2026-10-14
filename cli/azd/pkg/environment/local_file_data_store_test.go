// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_LocalFileDataStore_RejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../../trusted-project/.azure/prod",
		`..\..\trusted-project\.azure\prod`, "dev/../prod", `dev\prod`} {
		t.Run(name, func(t *testing.T) {
			for _, operation := range []string{"Get", "Reload", "Save", "Delete", "EnvPath", "ConfigPath"} {
				t.Run(operation, func(t *testing.T) {
					dir := t.TempDir()
					azdContext := azdcontext.NewAzdContextWithDirectory(filepath.Join(dir, "low-project"))
					store := NewLocalFileDataStore(azdContext, config.NewFileConfigManager(config.NewManager()))
					target := filepath.Join(azdContext.EnvironmentDirectory(), name)
					require.NoError(t, os.MkdirAll(target, 0700))
					envBytes := []byte("TRUSTED_MARKER=unchanged\n")
					configBytes := []byte(`{"trusted":"unchanged"}`)
					envPath := filepath.Join(target, DotEnvFileName)
					configPath := filepath.Join(target, ConfigFileName)
					require.NoError(t, os.WriteFile(envPath, envBytes, 0600))
					require.NoError(t, os.WriteFile(configPath, configBytes, 0600))

					env := New(name)
					env.DotenvSet("TRUSTED_MARKER", "modified")
					var err error
					switch operation {
					case "Get":
						var loaded *Environment
						loaded, err = store.Get(t.Context(), name)
						require.Nil(t, loaded)
					case "Reload":
						err = store.Reload(t.Context(), env)
						require.Equal(t, "modified", env.Getenv("TRUSTED_MARKER"))
					case "Save":
						err = store.Save(t.Context(), env, nil)
					case "Delete":
						err = store.Delete(t.Context(), name)
					case "EnvPath":
						var path string
						path, err = store.EnvPath(env)
						require.Empty(t, path)
					case "ConfigPath":
						var path string
						path, err = store.ConfigPath(env)
						require.Empty(t, path)
					}
					require.ErrorContains(t, err, "is invalid")
					actualEnv, err := os.ReadFile(envPath)
					require.NoError(t, err)
					require.Equal(t, envBytes, actualEnv)
					actualConfig, err := os.ReadFile(configPath)
					require.NoError(t, err)
					require.Equal(t, configBytes, actualConfig)
					require.NoFileExists(t, filepath.Join(target, DotEnvFileName+".lock"))
				})
			}
		})
	}
}

func Test_LocalFileDataStore_InvalidNameDoesNotCreateDirectory(t *testing.T) {
	for _, operation := range []string{"Reload", "Save"} {
		t.Run(operation, func(t *testing.T) {
			dir := t.TempDir()
			azdContext := azdcontext.NewAzdContextWithDirectory(filepath.Join(dir, "low-project"))
			store := NewLocalFileDataStore(azdContext, config.NewFileConfigManager(config.NewManager()))
			env := New("../../new-project/prod")
			var err error
			if operation == "Reload" {
				err = store.Reload(t.Context(), env)
			} else {
				err = store.Save(t.Context(), env, nil)
			}
			require.ErrorContains(t, err, "is invalid")
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func Test_LocalFileDataStore_List(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	dataStore := NewLocalFileDataStore(azdContext, fileConfigManager)

	t.Run("List", func(t *testing.T) {
		env1 := New("env1")
		err := dataStore.Save(*mockContext.Context, env1, nil)
		require.NoError(t, err)

		env2 := New("env2")
		err = dataStore.Save(*mockContext.Context, env2, nil)
		require.NoError(t, err)

		envList, err := dataStore.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
		require.Equal(t, 2, len(envList))
	})

	t.Run("Empty", func(t *testing.T) {
		envList, err := dataStore.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
	})
}

func Test_LocalFileDataStore_SaveAndGet(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	dataStore := NewLocalFileDataStore(azdContext, fileConfigManager)

	t.Run("Success", func(t *testing.T) {
		env1 := New("env1")
		env1.DotenvSet("key1", "value1")
		err := dataStore.Save(*mockContext.Context, env1, nil)
		require.NoError(t, err)

		env, err := dataStore.Get(*mockContext.Context, "env1")
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, "env1", env.name)
		actual := env1.Getenv("key1")
		require.Equal(t, "value1", actual)
	})
}

func Test_LocalFileDataStore_Path(t *testing.T) {
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	dataStore := NewLocalFileDataStore(azdContext, fileConfigManager)

	env := New("env1")
	root, err := azdContext.EnvironmentRoot("env1")
	require.NoError(t, err)
	expected := filepath.Join(root, DotEnvFileName)
	actual, err := dataStore.EnvPath(env)
	require.NoError(t, err)

	require.Equal(t, expected, actual)
}

func Test_LocalFileDataStore_ConfigPath(t *testing.T) {
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	dataStore := NewLocalFileDataStore(azdContext, fileConfigManager)

	env := New("env1")
	root, err := azdContext.EnvironmentRoot("env1")
	require.NoError(t, err)
	expected := filepath.Join(root, ConfigFileName)
	actual, err := dataStore.ConfigPath(env)
	require.NoError(t, err)

	require.Equal(t, expected, actual)
}

func TestLocalReloadInvalidConfigPreservesDotenv(t *testing.T) {
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	store := NewLocalFileDataStore(azdContext, config.NewFileConfigManager(config.NewManager()))
	env := New("test")
	root, err := azdContext.EnvironmentRoot("test")
	require.NoError(t, err)
	envPath, err := store.EnvPath(env)
	require.NoError(t, err)
	configPath, err := store.ConfigPath(env)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(root, 0700))
	require.NoError(t, os.WriteFile(envPath, []byte("VALUE=on-disk\n"), 0600))
	require.NoError(t, os.WriteFile(configPath, []byte("{invalid"), 0600))
	env.DotenvSet("VALUE", "in-memory")
	env.DotenvDelete("PENDING")

	require.ErrorContains(t, store.Reload(t.Context(), env), "loading config")
	require.Equal(t, "in-memory", env.Getenv("VALUE"))
	require.Contains(t, env.deletedKeys, "PENDING")
}

// Test_LocalFileDataStore_ConcurrentSave_NoLostUpdate is a regression test
// for the cross-process write race that existed before the OS-level flock
// + atomic-rename was added to Save (see #7776 review thread H1).
//
// Pre-fix: two LocalFileDataStore instances against the same .env file would
// each Reload, merge their own keys, then `os.Create`-truncate-and-write —
// last writer silently clobbered the first writer's keys. This mirrors the
// real cross-process scenario where parallel service hooks spawn `azd env
// set` subprocesses, each owning its own in-process `saveMu` but sharing
// the same on-disk file.
//
// With flock + atomic rename, both writers' keys must be present in the
// final file regardless of interleaving.
func Test_LocalFileDataStore_ConcurrentSave_NoLostUpdate(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	dir := t.TempDir()
	fileConfigManager := config.NewFileConfigManager(config.NewManager())

	// Seed the env directory with a save through one store so the env
	// root exists for both writers.
	seedCtx := azdcontext.NewAzdContextWithDirectory(dir)
	seedStore := NewLocalFileDataStore(seedCtx, fileConfigManager)
	seedEnv := New("concurrent")
	require.NoError(t, seedStore.Save(*mockContext.Context, seedEnv, nil))

	const writers = 8
	const keysPerWriter = 5

	// Each goroutine builds its own LocalFileDataStore + Environment,
	// bypassing any in-process synchronisation on a shared instance and
	// reproducing the cross-process race.
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(writerIdx int) {
			defer wg.Done()
			ctx := azdcontext.NewAzdContextWithDirectory(dir)
			store := NewLocalFileDataStore(ctx, fileConfigManager)
			env, err := store.Get(*mockContext.Context, "concurrent")
			if err != nil {
				t.Errorf("writer %d Get: %v", writerIdx, err)
				return
			}
			for k := range keysPerWriter {
				env.DotenvSet(fmt.Sprintf("W%d_K%d", writerIdx, k), fmt.Sprintf("v%d", k))
			}
			if err := store.Save(*mockContext.Context, env, nil); err != nil {
				t.Errorf("writer %d Save: %v", writerIdx, err)
			}
		}(w)
	}
	wg.Wait()

	// Final read must see every key from every writer.
	final, err := seedStore.Get(*mockContext.Context, "concurrent")
	require.NoError(t, err)
	for w := range writers {
		for k := range keysPerWriter {
			key := fmt.Sprintf("W%d_K%d", w, k)
			require.Equal(t, fmt.Sprintf("v%d", k), final.Getenv(key),
				"key %s missing — concurrent Save lost an update", key)
		}
	}
}
