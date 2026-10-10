// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/joho/godotenv"
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

func TestLocalReloadInvalidConfigPreservesDotenv(t *testing.T) {
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	store := NewLocalFileDataStore(azdContext, config.NewFileConfigManager(config.NewManager()))
	env := New("test")
	require.NoError(t, os.MkdirAll(azdContext.EnvironmentRoot("test"), 0700))
	require.NoError(t, os.WriteFile(store.EnvPath(env), []byte("VALUE=on-disk\n"), 0600))
	require.NoError(t, os.WriteFile(store.ConfigPath(env), []byte("{invalid"), 0600))
	env.DotenvSet("VALUE", "in-memory")
	env.DotenvDelete("PENDING")

	require.ErrorContains(t, store.Reload(t.Context(), env), "loading config")
	require.Equal(t, "in-memory", env.Getenv("VALUE"))
	require.Contains(t, env.deletedKeys, "PENDING")
}

func TestLocalFileDataStoreSaveSelectedKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		options      *SaveOptions
		fullSave     bool
		saveSelected bool
	}{
		{name: "NilOptions", fullSave: true, saveSelected: true},
		{name: "DefaultOptions", options: &SaveOptions{}, fullSave: true, saveSelected: true},
		{
			name: "SelectedKeys", options: &SaveOptions{DotenvKeys: []string{"REMOVE", "RESTORE", "LD_SELECTED"}},
			saveSelected: true,
		},
		{name: "NoSelectedKeys", options: &SaveOptions{DotenvKeys: []string{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
			store := NewLocalFileDataStore(azdCtx, config.NewFileConfigManager(config.NewManager()))
			seed := NewWithValues("test", map[string]string{
				"REMOVE":            "remove-me",
				"RESTORE":           "original",
				"LD_SELECTED":       "raw-original",
				"DYLD_KEEP":         "raw-unrelated",
				"KEEP":              "original",
				"OTHER_REMOVED":     "original",
				"UNSELECTED_DELETE": "original",
			})
			require.NoError(t, seed.Config.Set("app.enabled", true))
			require.NoError(t, store.Save(t.Context(), seed, nil))
			env, err := store.Get(t.Context(), "test")
			require.NoError(t, err)
			concurrent, err := store.Get(t.Context(), "test")
			require.NoError(t, err)
			concurrent.DotenvSet("KEEP", "concurrent-value")
			concurrent.DotenvSet("ADDED", "concurrent-addition")
			concurrent.DotenvDelete("OTHER_REMOVED")
			require.NoError(t, concurrent.Config.Set("app.enabled", false))
			require.NoError(t, store.Save(t.Context(), concurrent, nil))
			want, err := godotenv.Read(store.EnvPath(env))
			require.NoError(t, err)
			configBefore, err := os.ReadFile(store.ConfigPath(env))
			require.NoError(t, err)

			env.DotenvDelete("REMOVE")
			env.DotenvSet("RESTORE", "restored")
			env.DotenvSet("LD_SELECTED", "raw-updated")
			env.DotenvSet("DYLD_KEEP", "pending")
			env.DotenvSet("PENDING", "pending")
			env.DotenvDelete("UNSELECTED_DELETE")
			require.NoError(t, store.Save(t.Context(), env, tt.options))
			if tt.fullSave {
				maps.Copy(want, map[string]string{
					"KEEP":          "original",
					"OTHER_REMOVED": "original",
					"DYLD_KEEP":     "pending",
					"PENDING":       "pending",
				})
				delete(want, "UNSELECTED_DELETE")
			}
			if tt.saveSelected {
				delete(want, "REMOVE")
				want["RESTORE"] = "restored"
				want["LD_SELECTED"] = "raw-updated"
			}
			persisted, err := godotenv.Read(store.EnvPath(env))
			require.NoError(t, err)
			require.Equal(t, want, persisted)
			for key, value := range want {
				actual, exists := env.LookupDotenv(key)
				require.True(t, exists)
				require.Equal(t, value, actual)
			}
			configAfter, err := os.ReadFile(store.ConfigPath(env))
			require.NoError(t, err)
			if tt.fullSave {
				require.JSONEq(t, `{"app":{"enabled":true}}`, string(configAfter))
			} else {
				require.Equal(t, configBefore, configAfter)
			}
		})
	}
}

func TestLocalFileDataStoreSaveSelectedKeysInvalidConfig(t *testing.T) {
	t.Parallel()
	azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	store := NewLocalFileDataStore(azdCtx, config.NewFileConfigManager(config.NewManager()))
	env := NewWithValues("test", map[string]string{"KEY": "original"})
	require.NoError(t, store.Save(t.Context(), env, nil))
	before, err := os.ReadFile(store.EnvPath(env))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(store.ConfigPath(env), []byte("{invalid"), 0600))
	env.DotenvDelete("KEY")

	require.ErrorContains(t, store.Save(t.Context(), env, &SaveOptions{DotenvKeys: []string{"KEY"}}), "loading config")
	after, err := os.ReadFile(store.EnvPath(env))
	require.NoError(t, err)
	require.Equal(t, before, after)
	cfg, err := os.ReadFile(store.ConfigPath(env))
	require.NoError(t, err)
	require.Equal(t, "{invalid", string(cfg))
}

func TestLocalFileDataStoreConcurrentSelectedKeysSave(t *testing.T) {
	t.Parallel()
	azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	seedStore := NewLocalFileDataStore(azdCtx, fileConfigManager)
	seed := NewWithValues("test", map[string]string{"KEEP": "unchanged"})
	const writers = 8
	for i := range writers {
		seed.DotenvSet(fmt.Sprintf("UPDATE_%d", i), "original")
		seed.DotenvSet(fmt.Sprintf("REMOVE_%d", i), "original")
	}
	require.NoError(t, seedStore.Save(t.Context(), seed, nil))

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range writers {
		store := NewLocalFileDataStore(azdCtx, fileConfigManager)
		env, err := store.Get(t.Context(), "test")
		require.NoError(t, err)
		updateKey := fmt.Sprintf("UPDATE_%d", i)
		removeKey := fmt.Sprintf("REMOVE_%d", i)
		env.DotenvSet(updateKey, "updated")
		env.DotenvDelete(removeKey)
		wg.Go(func() {
			<-start
			if err := store.Save(t.Context(), env, &SaveOptions{DotenvKeys: []string{updateKey, removeKey}}); err != nil {
				t.Errorf("writer %d Save: %v", i, err)
			}
		})
	}
	close(start)
	wg.Wait()

	final, err := seedStore.Get(t.Context(), "test")
	require.NoError(t, err)
	want := map[string]string{"KEEP": "unchanged"}
	for i := range writers {
		want[fmt.Sprintf("UPDATE_%d", i)] = "updated"
	}
	require.Equal(t, want, final.Dotenv())
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
