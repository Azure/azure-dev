// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

const (
	userConfigLockFileName    = "config.lock"
	userConfigLockWaitTimeout = 30 * time.Second
	userConfigCommitTimeout   = 30 * time.Second
	userConfigLockRetry       = 50 * time.Millisecond
)

var userConfigGates sync.Map

type userConfigMutationContextKey struct{}

type userConfigGate struct {
	token chan struct{}
}

func newUserConfigGate() *userConfigGate {
	gate := &userConfigGate{token: make(chan struct{}, 1)}
	gate.token <- struct{}{}
	return gate
}

func (g *userConfigGate) acquire(ctx context.Context) error {
	select {
	case <-g.token:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *userConfigGate) release() {
	g.token <- struct{}{}
}

type UserConfigManager interface {
	Load() (Config, error)
	Mutate(ctx context.Context, mutation func(context.Context, Config) (bool, error)) error
	Replace(ctx context.Context, replacement Config) error
}

type userConfigManager struct {
	manager         FileConfigManager
	lockWaitTimeout time.Duration
	commitTimeout   time.Duration
}

func NewUserConfigManager(configManager FileConfigManager) UserConfigManager {
	return &userConfigManager{
		manager:         configManager,
		lockWaitTimeout: userConfigLockWaitTimeout,
		commitTimeout:   userConfigCommitTimeout,
	}
}

func (m *userConfigManager) Load() (Config, error) {
	configFilePath, err := GetUserConfigFilePath()
	if err != nil {
		return nil, err
	}

	return m.load(configFilePath)
}

func (m *userConfigManager) load(configFilePath string) (Config, error) {
	azdConfig, err := m.manager.Load(configFilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, statErr := os.Stat(configFilePath); errors.Is(statErr, os.ErrNotExist) {
				log.Printf("creating empty config since '%s' did not exist.", configFilePath)
				return NewConfig(nil), nil
			}
		}

		return nil, fmt.Errorf("failed loading azd user config from '%s'. %w", configFilePath, err)
	}

	return azdConfig, nil
}

func (m *userConfigManager) Mutate(
	ctx context.Context,
	mutation func(context.Context, Config) (bool, error),
) error {
	if mutation == nil {
		return errors.New("user config mutation must not be nil")
	}
	if ctx.Value(userConfigMutationContextKey{}) != nil {
		return errors.New("user config mutations must not be nested")
	}

	userConfigFilePath, err := GetUserConfigFilePath()
	if err != nil {
		return fmt.Errorf("failed getting user config file path. %w", err)
	}

	return m.withLock(ctx, userConfigFilePath, func(commitCtx context.Context) error {
		azdConfig, err := m.load(userConfigFilePath)
		if err != nil {
			return err
		}

		mutationCtx := context.WithValue(commitCtx, userConfigMutationContextKey{}, true)
		changed, err := mutation(mutationCtx, azdConfig)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}

		if err := m.manager.SaveWithContext(commitCtx, azdConfig, userConfigFilePath); err != nil {
			return fmt.Errorf("failed saving configuration: %w", err)
		}

		return nil
	})
}

func (m *userConfigManager) Replace(ctx context.Context, replacement Config) error {
	if replacement == nil {
		return errors.New("replacement user config must not be nil")
	}
	if ctx.Value(userConfigMutationContextKey{}) != nil {
		return errors.New("user config mutations must not be nested")
	}
	baseConfig, ok := replacement.(*config)
	if !ok {
		return fmt.Errorf("failed casting replacement user configuration to config")
	}
	if baseConfig.vaultId != "" {
		return errors.New("replacing user configuration with vault-backed data is not supported")
	}

	userConfigFilePath, err := GetUserConfigFilePath()
	if err != nil {
		return fmt.Errorf("failed getting user config file path. %w", err)
	}

	return m.withLock(ctx, userConfigFilePath, func(commitCtx context.Context) error {
		if err := m.manager.SaveWithContext(commitCtx, replacement, userConfigFilePath); err != nil {
			return fmt.Errorf("failed replacing configuration: %w", err)
		}

		return nil
	})
}

func (m *userConfigManager) withLock(
	ctx context.Context,
	userConfigFilePath string,
	action func(context.Context) error,
) error {
	lockCtx, cancelLock := context.WithTimeout(ctx, m.lockWaitTimeout)
	defer cancelLock()

	gateValue, _ := userConfigGates.LoadOrStore(userConfigFilePath, newUserConfigGate())
	gate := gateValue.(*userConfigGate)
	if err := gate.acquire(lockCtx); err != nil {
		return fmt.Errorf("waiting for local user config lock: %w", err)
	}
	defer gate.release()

	fileLock := flock.New(filepath.Join(filepath.Dir(userConfigFilePath), userConfigLockFileName))
	locked, err := fileLock.TryLockContext(lockCtx, userConfigLockRetry)
	if err != nil {
		return fmt.Errorf("locking user configuration: %w", err)
	}
	if !locked {
		if err := lockCtx.Err(); err != nil {
			return fmt.Errorf("locking user configuration: %w", err)
		}
		return errors.New("locking user configuration: lock was not acquired")
	}
	defer func() {
		if err := fileLock.Unlock(); err != nil {
			log.Printf("failed to release user config lock: %v", err)
		}
	}()

	commitCtx, cancelCommit := context.WithTimeout(context.WithoutCancel(ctx), m.commitTimeout)
	defer cancelCommit()
	if err := action(commitCtx); err != nil {
		return err
	}

	return nil
}

// Gets the local file system path to the Azd configuration file
func GetUserConfigFilePath() (string, error) {
	configPath, err := GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed getting user config file path '%s'. %w", configPath, err)
	}

	return filepath.Join(configPath, "config.json"), nil
}
