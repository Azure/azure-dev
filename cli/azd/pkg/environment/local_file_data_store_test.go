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
			envPath, err := store.EnvPath(env)
			require.NoError(t, err)
			configPath, err := store.ConfigPath(env)
			require.NoError(t, err)
			concurrent, err := store.Get(t.Context(), "test")
			require.NoError(t, err)
			concurrent.DotenvSet("KEEP", "concurrent-value")
			concurrent.DotenvSet("ADDED", "concurrent-addition")
			concurrent.DotenvDelete("OTHER_REMOVED")
			require.NoError(t, concurrent.Config.Set("app.enabled", false))
			require.NoError(t, store.Save(t.Context(), concurrent, nil))
			want, err := godotenv.Read(envPath)
			require.NoError(t, err)
			configBefore, err := os.ReadFile(configPath)
			require.NoError(t, err)

			env.DotenvDelete("REMOVE")
			env.DotenvSet("RESTORE", "restored")
			env.DotenvSet("LD_SELECTED", "raw-updated")
			env.DotenvSet("DYLD_KEEP", "pending")
			env.DotenvSet("PENDING", "pending")
			env.DotenvDelete("UNSELECTED_DELETE")
			env.mu.RLock()
			memoryBefore := maps.Clone(env.dotenv)
			env.mu.RUnlock()
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
			persisted, err := godotenv.Read(envPath)
			require.NoError(t, err)
			require.Equal(t, want, persisted)
			memoryWant := memoryBefore
			if tt.fullSave {
				memoryWant = want
			}
			marshalled, err := marshallDotEnv(env)
			require.NoError(t, err)
			memoryAfter, err := godotenv.Unmarshal(marshalled)
			require.NoError(t, err)
			require.Equal(t, memoryWant, memoryAfter)
			configAfter, err := os.ReadFile(configPath)
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
	envPath, err := store.EnvPath(env)
	require.NoError(t, err)
	configPath, err := store.ConfigPath(env)
	require.NoError(t, err)
	before, err := os.ReadFile(envPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, []byte("{invalid"), 0600))
	env.DotenvDelete("KEY")

	require.ErrorContains(t, store.Save(t.Context(), env, &SaveOptions{DotenvKeys: []string{"KEY"}}), "loading config")
	require.Contains(t, env.deletedKeys, "KEY", "failed saves must retain pending deletions")
	after, err := os.ReadFile(envPath)
	require.NoError(t, err)
	require.Equal(t, before, after)
	cfg, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.Equal(t, "{invalid", string(cfg))
}

func TestLocalFileDataStoreSaveSelectedKeysPreservesPendingChanges(t *testing.T) {
	t.Parallel()
	for _, keys := range [][]string{{"REMOVE", "RESTORE"}, {}} {
		name := "SelectedKeys"
		if len(keys) == 0 {
			name = "NoSelectedKeys"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
			store := NewLocalFileDataStore(azdCtx, config.NewFileConfigManager(config.NewManager()))
			seedValues := map[string]string{
				"REMOVE": "original", "RESTORE": "original",
				"PENDING_UPDATE": "original", "PENDING_DELETE": "original",
			}
			env := NewWithValues("test", maps.Clone(seedValues))
			require.NoError(t, env.Config.Set("app.enabled", true))
			require.NoError(t, store.Save(t.Context(), env, nil))
			envPath, err := store.EnvPath(env)
			require.NoError(t, err)
			configPath, err := store.ConfigPath(env)
			require.NoError(t, err)
			env.DotenvDelete("REMOVE")
			env.DotenvSet("RESTORE", "restored")
			env.DotenvSet("PENDING_UPDATE", "updated")
			env.DotenvSet("PENDING_ADD", "added")
			env.DotenvSet("DYLD_PENDING", "raw-pending")
			env.DotenvDelete("PENDING_DELETE")
			require.NoError(t, env.Config.Set("app.enabled", false))

			require.NoError(t, store.Save(t.Context(), env, &SaveOptions{DotenvKeys: keys}))
			value, exists := env.LookupDotenv("PENDING_ADD")
			require.True(t, exists, "an unrelated pending addition must survive a selected-key save")
			require.Equal(t, "added", value)
			require.Equal(t, "updated", env.Getenv("PENDING_UPDATE"))
			value, exists = env.LookupDotenv("DYLD_PENDING")
			require.True(t, exists)
			require.Equal(t, "raw-pending", value)
			_, exists = env.LookupDotenv("PENDING_DELETE")
			require.False(t, exists)
			require.Contains(t, env.deletedKeys, "PENDING_DELETE")
			require.Equal(t, map[string]any{"app": map[string]any{"enabled": false}}, env.Config.Raw())

			want := maps.Clone(seedValues)
			if len(keys) > 0 {
				delete(want, "REMOVE")
				want["RESTORE"] = "restored"
				require.NotContains(t, env.deletedKeys, "REMOVE", "committed deletions must be acknowledged")
			} else {
				require.Contains(t, env.deletedKeys, "REMOVE")
			}
			persisted, err := godotenv.Read(envPath)
			require.NoError(t, err)
			require.Equal(t, want, persisted)
			cfg, err := os.ReadFile(configPath)
			require.NoError(t, err)
			require.JSONEq(t, `{"app":{"enabled":true}}`, string(cfg))

			finalWant := map[string]string{
				"RESTORE": "restored", "PENDING_UPDATE": "updated",
				"PENDING_ADD": "added", "DYLD_PENDING": "raw-pending",
			}
			if len(keys) > 0 {
				want["REMOVE"] = "concurrent-reintroduction"
				require.NoError(t, godotenv.Write(want, envPath))
				finalWant["REMOVE"] = "concurrent-reintroduction"
			}
			require.NoError(t, store.Save(t.Context(), env, nil))
			persisted, err = godotenv.Read(envPath)
			require.NoError(t, err)
			require.Equal(t, finalWant, persisted)
			cfg, err = os.ReadFile(configPath)
			require.NoError(t, err)
			require.JSONEq(t, `{"app":{"enabled":false}}`, string(cfg))
		})
	}
}

func TestLocalFileDataStoreConcurrentSelectedKeysSaveSharedEnvironment(t *testing.T) {
	t.Parallel()
	azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	store := NewLocalFileDataStore(azdCtx, config.NewFileConfigManager(config.NewManager()))
	env := New("test")
	env.DotenvSet("KEEP", "unchanged")
	const writers = 8
	for i := range writers {
		env.DotenvSet(fmt.Sprintf("UPDATE_%d", i), "original")
		env.DotenvSet(fmt.Sprintf("REMOVE_%d", i), "original")
	}
	require.NoError(t, store.Save(t.Context(), env, nil))

	var ready, wg sync.WaitGroup
	ready.Add(writers)
	start := make(chan struct{})
	for i := range writers {
		wg.Go(func() {
			updateKey := fmt.Sprintf("UPDATE_%d", i)
			addKey := fmt.Sprintf("ADDED_%d", i)
			removeKey := fmt.Sprintf("REMOVE_%d", i)
			env.DotenvSet(updateKey, "updated")
			env.DotenvSet(addKey, "added")
			env.DotenvDelete(removeKey)
			ready.Done()
			<-start
			options := &SaveOptions{DotenvKeys: []string{updateKey, addKey, removeKey}}
			if err := store.Save(t.Context(), env, options); err != nil {
				t.Errorf("writer %d Save: %v", i, err)
			}
		})
	}
	ready.Wait()
	close(start)
	wg.Wait()

	final, err := store.Get(t.Context(), "test")
	require.NoError(t, err)
	want := map[string]string{EnvNameEnvVarName: "test", "KEEP": "unchanged"}
	for i := range writers {
		want[fmt.Sprintf("UPDATE_%d", i)] = "updated"
		want[fmt.Sprintf("ADDED_%d", i)] = "added"
	}
	require.Equal(t, want, final.Dotenv())
	require.Equal(t, want, env.Dotenv())
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
