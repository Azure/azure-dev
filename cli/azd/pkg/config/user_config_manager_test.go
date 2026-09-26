// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/require"
)

type countingFileConfigManager struct {
	FileConfigManager
	saveCount      int
	waitForTimeout bool
	loadErr        error
	saveErr        error
}

type legacyUserConfigManager struct {
	config    Config
	saveCount int
	loadErr   error
	saveErr   error
}

var _ UserConfigManager = (*legacyUserConfigManager)(nil)

func (m *legacyUserConfigManager) Load() (Config, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.config, nil
}

func (m *legacyUserConfigManager) Save(config Config) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.config = config
	m.saveCount++
	return nil
}

type legacyFileConfigManager struct {
	FileConfigManager
	saveCount int
}

var _ FileConfigManager = (*legacyFileConfigManager)(nil)

func (m *legacyFileConfigManager) Save(config Config, filePath string) error {
	m.saveCount++
	return m.FileConfigManager.Save(config, filePath)
}

func (m *countingFileConfigManager) Load(filePath string) (Config, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.FileConfigManager.Load(filePath)
}

func (m *countingFileConfigManager) SaveWithContext(ctx context.Context, cfg Config, filePath string) error {
	m.saveCount++
	if m.saveErr != nil {
		return m.saveErr
	}
	if m.waitForTimeout {
		<-ctx.Done()
		return ctx.Err()
	}
	return saveFileConfig(ctx, m.FileConfigManager, cfg, filePath)
}

func Test_UserConfigCompatibilityHelpers_LegacyManager(t *testing.T) {
	manager := &legacyUserConfigManager{config: NewEmptyConfig()}

	err := MutateUserConfig(t.Context(), manager, func(_ context.Context, config Config) (bool, error) {
		require.NoError(t, config.Set("mutated", true))
		return true, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, manager.saveCount)
	value, found := manager.config.Get("mutated")
	require.True(t, found)
	require.Equal(t, true, value)

	replacement := NewEmptyConfig()
	require.NoError(t, replacement.Set("replaced", true))
	require.NoError(t, ReplaceUserConfig(t.Context(), manager, replacement))
	require.Equal(t, 2, manager.saveCount)
	require.Same(t, replacement, manager.config)
}

func Test_UserConfigCompatibilityHelpers_LegacyManagerFailures(t *testing.T) {
	t.Run("nil mutation", func(t *testing.T) {
		manager := &legacyUserConfigManager{config: NewEmptyConfig()}
		err := MutateUserConfig(t.Context(), manager, nil)
		require.ErrorContains(t, err, "must not be nil")
	})

	t.Run("canceled before load", func(t *testing.T) {
		manager := &legacyUserConfigManager{config: NewEmptyConfig()}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		err := MutateUserConfig(ctx, manager, func(_ context.Context, _ Config) (bool, error) {
			return true, nil
		})
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("load failure", func(t *testing.T) {
		expectedErr := errors.New("load failed")
		manager := &legacyUserConfigManager{loadErr: expectedErr}

		err := MutateUserConfig(t.Context(), manager, func(_ context.Context, _ Config) (bool, error) {
			return true, nil
		})
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("mutation failure", func(t *testing.T) {
		expectedErr := errors.New("mutation failed")
		manager := &legacyUserConfigManager{config: NewEmptyConfig()}

		err := MutateUserConfig(t.Context(), manager, func(_ context.Context, _ Config) (bool, error) {
			return false, expectedErr
		})
		require.ErrorIs(t, err, expectedErr)
		require.Zero(t, manager.saveCount)
	})

	t.Run("unchanged", func(t *testing.T) {
		manager := &legacyUserConfigManager{config: NewEmptyConfig()}

		err := MutateUserConfig(t.Context(), manager, func(_ context.Context, _ Config) (bool, error) {
			return false, nil
		})
		require.NoError(t, err)
		require.Zero(t, manager.saveCount)
	})

	t.Run("canceled after mutation", func(t *testing.T) {
		manager := &legacyUserConfigManager{config: NewEmptyConfig()}
		ctx, cancel := context.WithCancel(t.Context())

		err := MutateUserConfig(ctx, manager, func(_ context.Context, _ Config) (bool, error) {
			cancel()
			return true, nil
		})
		require.ErrorIs(t, err, context.Canceled)
		require.Zero(t, manager.saveCount)
	})

	t.Run("save failure", func(t *testing.T) {
		expectedErr := errors.New("save failed")
		manager := &legacyUserConfigManager{
			config:  NewEmptyConfig(),
			saveErr: expectedErr,
		}

		err := MutateUserConfig(t.Context(), manager, func(_ context.Context, _ Config) (bool, error) {
			return true, nil
		})
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("nil replacement", func(t *testing.T) {
		manager := &legacyUserConfigManager{config: NewEmptyConfig()}
		err := ReplaceUserConfig(t.Context(), manager, nil)
		require.ErrorContains(t, err, "must not be nil")
	})

	t.Run("canceled replacement", func(t *testing.T) {
		manager := &legacyUserConfigManager{config: NewEmptyConfig()}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		err := ReplaceUserConfig(ctx, manager, NewEmptyConfig())
		require.ErrorIs(t, err, context.Canceled)
	})
}

func Test_UserConfigManager_LegacyFileManager(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	fileManager := &legacyFileConfigManager{
		FileConfigManager: NewFileConfigManager(NewManager()),
	}
	manager := NewUserConfigManager(fileManager)

	err := MutateUserConfig(t.Context(), manager, func(_ context.Context, config Config) (bool, error) {
		require.NoError(t, config.Set("legacy", true))
		return true, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, fileManager.saveCount)

	loaded, err := manager.Load()
	require.NoError(t, err)
	value, found := loaded.Get("legacy")
	require.True(t, found)
	require.Equal(t, true, value)
}

func Test_UserConfigManager_RejectsInvalidOperations(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	manager := NewUserConfigManager(NewFileConfigManager(NewManager())).(*userConfigManager)

	require.ErrorContains(t, manager.Mutate(t.Context(), nil), "must not be nil")

	nestedCtx := context.WithValue(t.Context(), userConfigMutationContextKey{}, true)
	err := manager.Mutate(nestedCtx, func(_ context.Context, _ Config) (bool, error) {
		return true, nil
	})
	require.ErrorContains(t, err, "must not be nested")

	require.ErrorContains(t, manager.Replace(t.Context(), nil), "must not be nil")
	err = manager.Replace(t.Context(), &configWithoutRawMapEntries{Config: NewEmptyConfig()})
	require.ErrorContains(t, err, "failed casting")
}

func Test_UserConfigManager_PropagatesFileManagerFailures(t *testing.T) {
	expectedErr := errors.New("file manager failed")

	t.Run("load", func(t *testing.T) {
		t.Setenv("AZD_CONFIG_DIR", t.TempDir())
		fileManager := &countingFileConfigManager{
			FileConfigManager: NewFileConfigManager(NewManager()),
			loadErr:           expectedErr,
		}
		manager := NewUserConfigManager(fileManager)

		err := MutateUserConfig(t.Context(), manager, func(_ context.Context, _ Config) (bool, error) {
			return true, nil
		})
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("mutation save", func(t *testing.T) {
		t.Setenv("AZD_CONFIG_DIR", t.TempDir())
		fileManager := &countingFileConfigManager{
			FileConfigManager: NewFileConfigManager(NewManager()),
			saveErr:           expectedErr,
		}
		manager := NewUserConfigManager(fileManager)

		err := MutateUserConfig(t.Context(), manager, func(_ context.Context, _ Config) (bool, error) {
			return true, nil
		})
		require.ErrorIs(t, err, expectedErr)
		require.ErrorContains(t, err, "failed saving configuration")
	})

	t.Run("replacement save", func(t *testing.T) {
		t.Setenv("AZD_CONFIG_DIR", t.TempDir())
		fileManager := &countingFileConfigManager{
			FileConfigManager: NewFileConfigManager(NewManager()),
			saveErr:           expectedErr,
		}
		manager := NewUserConfigManager(fileManager)

		err := ReplaceUserConfig(t.Context(), manager, NewEmptyConfig())
		require.ErrorIs(t, err, expectedErr)
		require.ErrorContains(t, err, "failed replacing configuration")
	})
}

func Test_UserConfigManager_SaveSupportsVaultBackedConfig(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	manager := NewUserConfigManager(NewFileConfigManager(NewManager()))
	cfg := NewEmptyConfig()
	require.NoError(t, cfg.SetSecret("secret", "value"))

	require.NoError(t, manager.Save(cfg))

	loaded, err := manager.Load()
	require.NoError(t, err)
	value, found := loaded.GetString("secret")
	require.True(t, found)
	require.Equal(t, "value", value)
}

func Test_UserConfigManager_MutationPublication(t *testing.T) {
	tests := []struct {
		name          string
		mutation      func(context.Context, Config) (bool, error)
		expectedSaves int
		expectedError error
	}{
		{
			name: "changed",
			mutation: func(_ context.Context, cfg Config) (bool, error) {
				if err := cfg.Set("changed", true); err != nil {
					return false, err
				}
				return true, nil
			},
			expectedSaves: 1,
		},
		{
			name: "unchanged",
			mutation: func(_ context.Context, _ Config) (bool, error) {
				return false, nil
			},
		},
		{
			name: "error",
			mutation: func(_ context.Context, _ Config) (bool, error) {
				return true, context.Canceled
			},
			expectedError: context.Canceled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AZD_CONFIG_DIR", t.TempDir())
			fileManager := &countingFileConfigManager{
				FileConfigManager: NewFileConfigManager(NewManager()),
			}
			manager := NewUserConfigManager(fileManager)

			err := MutateUserConfig(t.Context(), manager, test.mutation)
			if test.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.expectedError)
			}
			require.Equal(t, test.expectedSaves, fileManager.saveCount)
		})
	}
}

func Test_UserConfigManager_CommitUsesSeparateTimeout(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	fileManager := &countingFileConfigManager{
		FileConfigManager: NewFileConfigManager(NewManager()),
		waitForTimeout:    true,
	}
	manager := NewUserConfigManager(fileManager).(*userConfigManager)
	manager.commitTimeout = 25 * time.Millisecond

	start := time.Now()
	err := MutateUserConfig(t.Context(), manager, func(_ context.Context, cfg Config) (bool, error) {
		require.NoError(t, cfg.Set("value", true))
		return true, nil
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, 1, fileManager.saveCount)
}

func Test_UserConfigManager_LockWaitUsesSeparateTimeout(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	manager := NewUserConfigManager(NewFileConfigManager(NewManager())).(*userConfigManager)
	manager.lockWaitTimeout = 25 * time.Millisecond

	fileLock := flock.New(filepath.Join(configDir, userConfigLockFileName))
	require.NoError(t, fileLock.Lock())
	defer func() {
		require.NoError(t, fileLock.Unlock())
	}()

	start := time.Now()
	err := MutateUserConfig(t.Context(), manager, func(_ context.Context, _ Config) (bool, error) {
		return true, nil
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second)
}

func Test_UserConfigManager_ConcurrentMutationsPreserveAllWrites(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	managers := []UserConfigManager{
		NewUserConfigManager(NewFileConfigManager(NewManager())),
		NewUserConfigManager(NewFileConfigManager(NewManager())),
	}

	const writers = 24
	start := make(chan struct{})
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			<-start
			manager := managers[i%len(managers)]
			errs <- MutateUserConfig(t.Context(), manager, func(_ context.Context, cfg Config) (bool, error) {
				time.Sleep(2 * time.Millisecond)
				if err := cfg.Set(fmt.Sprintf("concurrent.key%d", i), i); err != nil {
					return false, err
				}
				return true, nil
			})
		})
	}

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	cfg, err := managers[0].Load()
	require.NoError(t, err)
	for i := range writers {
		value, found := cfg.Get(fmt.Sprintf("concurrent.key%d", i))
		require.True(t, found)
		require.Equal(t, float64(i), value)
	}
}

func Test_UserConfigManager_SubprocessWorker(t *testing.T) {
	key := os.Getenv("AZD_TEST_USER_CONFIG_KEY")
	if key == "" {
		t.Skip("subprocess worker")
	}

	manager := NewUserConfigManager(NewFileConfigManager(NewManager()))
	err := MutateUserConfig(t.Context(), manager, func(_ context.Context, cfg Config) (bool, error) {
		time.Sleep(10 * time.Millisecond)
		if err := cfg.Set("processes."+key, key); err != nil {
			return false, err
		}
		return true, nil
	})
	require.NoError(t, err)
}

func Test_UserConfigManager_ConcurrentProcessesPreserveAllWrites(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	const writers = 8

	commands := make([]*exec.Cmd, 0, writers)
	for i := range writers {
		key := fmt.Sprintf("key%d", i)
		//nolint:gosec // os.Args[0] is the current test binary.
		command := exec.Command(os.Args[0], "-test.run=^Test_UserConfigManager_SubprocessWorker$")
		command.Env = append(
			os.Environ(),
			"AZD_CONFIG_DIR="+configDir,
			"AZD_TEST_USER_CONFIG_KEY="+key,
		)
		require.NoError(t, command.Start())
		commands = append(commands, command)
	}

	for _, command := range commands {
		require.NoError(t, command.Wait())
	}

	manager := NewUserConfigManager(NewFileConfigManager(NewManager()))
	cfg, err := manager.Load()
	require.NoError(t, err)
	for i := range writers {
		key := fmt.Sprintf("key%d", i)
		value, found := cfg.Get("processes." + key)
		require.True(t, found)
		require.Equal(t, key, value)
	}
}

func Test_UserConfigManager_LockWaitHonorsCancellation(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	manager := NewUserConfigManager(NewFileConfigManager(NewManager()))

	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- MutateUserConfig(t.Context(), manager, func(_ context.Context, cfg Config) (bool, error) {
			close(entered)
			<-release
			if err := cfg.Set("first", true); err != nil {
				return false, err
			}
			return true, nil
		})
	}()
	<-entered

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	err := MutateUserConfig(ctx, manager, func(_ context.Context, cfg Config) (bool, error) {
		if err := cfg.Set("second", true); err != nil {
			return false, err
		}
		return true, nil
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	close(release)
	require.NoError(t, <-firstDone)
}

func Test_UserConfigManager_AlreadyCanceledContextDoesNotEnterMutation(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	manager := NewUserConfigManager(NewFileConfigManager(NewManager()))

	mutationCalls := 0
	for range 100 {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		err := MutateUserConfig(ctx, manager, func(_ context.Context, _ Config) (bool, error) {
			mutationCalls++
			return true, nil
		})
		require.ErrorIs(t, err, context.Canceled)
	}
	require.Zero(t, mutationCalls)
}

func Test_UserConfigManager_FileLockWaitHonorsCancellation(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	manager := NewUserConfigManager(NewFileConfigManager(NewManager()))

	fileLock := flock.New(filepath.Join(configDir, userConfigLockFileName))
	require.NoError(t, fileLock.Lock())
	defer func() {
		require.NoError(t, fileLock.Unlock())
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	err := MutateUserConfig(ctx, manager, func(_ context.Context, cfg Config) (bool, error) {
		if err := cfg.Set("value", true); err != nil {
			return false, err
		}
		return true, nil
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func Test_UserConfigManager_RejectsNestedMutation(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	manager := NewUserConfigManager(NewFileConfigManager(NewManager()))

	err := MutateUserConfig(t.Context(), manager, func(txCtx context.Context, _ Config) (bool, error) {
		if err := ReplaceUserConfig(txCtx, manager, NewEmptyConfig()); err != nil {
			return false, err
		}
		return true, nil
	})
	require.ErrorContains(t, err, "must not be nested")
}

func Test_UserConfigManager_MissingReferencedVaultIsNotEmptyConfig(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "config.json"),
		[]byte(`{"vault":"missing-vault"}`),
		0o600,
	))

	manager := NewUserConfigManager(NewFileConfigManager(NewManager()))
	_, err := manager.Load()
	require.Error(t, err)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "failed loading vault configuration")
}

func Test_UserConfigManager_ReplaceRejectsVaultBackedConfig(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	manager := NewUserConfigManager(NewFileConfigManager(NewManager()))
	cfg := NewEmptyConfig()
	require.NoError(t, cfg.SetSecret("secret", "value"))

	err := ReplaceUserConfig(t.Context(), manager, cfg)
	require.ErrorContains(t, err, "vault-backed")
}
