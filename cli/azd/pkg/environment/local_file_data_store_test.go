// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

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
	expected := filepath.Join(azdContext.EnvironmentRoot("env1"), DotEnvFileName)
	actual := dataStore.EnvPath(env)

	require.Equal(t, expected, actual)
}

func Test_LocalFileDataStore_ConfigPath(t *testing.T) {
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	dataStore := NewLocalFileDataStore(azdContext, fileConfigManager)

	env := New("env1")
	expected := filepath.Join(azdContext.EnvironmentRoot("env1"), ConfigFileName)
	actual := dataStore.ConfigPath(env)

	require.Equal(t, expected, actual)
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
