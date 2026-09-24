// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// Config returns a live, synchronized view of the environment configuration.
// Reads return detached values, and writes copy caller-owned values into the
// live config. Retained views use the current configuration after ReplaceState.
// Use SnapshotState for a detached persistence snapshot.
func (e *Environment) Config() config.Config {
	return &environmentConfig{env: e}
}

type environmentConfig struct {
	env *Environment
}

var _ config.Config = (*environmentConfig)(nil)

func (c *environmentConfig) Raw() map[string]any {
	c.env.mu.RLock()
	defer c.env.mu.RUnlock()
	return cloneConfigValue(c.env.config.Raw())
}

func (c *environmentConfig) ResolvedRaw() map[string]any {
	c.env.mu.RLock()
	defer c.env.mu.RUnlock()
	return cloneConfigValue(c.env.config.ResolvedRaw())
}

func (c *environmentConfig) Get(path string) (any, bool) {
	c.env.mu.RLock()
	defer c.env.mu.RUnlock()
	value, ok := c.env.config.Get(path)
	return cloneConfigValue(value), ok
}

func (c *environmentConfig) GetString(path string) (string, bool) {
	c.env.mu.RLock()
	defer c.env.mu.RUnlock()
	return c.env.config.GetString(path)
}

func (c *environmentConfig) GetSection(path string, section any) (bool, error) {
	c.env.mu.RLock()
	defer c.env.mu.RUnlock()
	return c.env.config.GetSection(path, section)
}

func (c *environmentConfig) GetMap(path string) (map[string]any, bool) {
	c.env.mu.RLock()
	defer c.env.mu.RUnlock()
	value, ok := c.env.config.GetMap(path)
	return cloneConfigValue(value), ok
}

func (c *environmentConfig) GetSlice(path string) ([]any, bool) {
	c.env.mu.RLock()
	defer c.env.mu.RUnlock()
	value, ok := c.env.config.GetSlice(path)
	return cloneConfigValue(value), ok
}

func (c *environmentConfig) Set(path string, value any) error {
	cloned, err := config.CloneValue(value)
	if err != nil {
		return fmt.Errorf("setting environment configuration: %w", err)
	}
	c.env.mu.Lock()
	defer c.env.mu.Unlock()
	return c.env.config.Set(path, cloned)
}

func (c *environmentConfig) SetSecret(path string, value string) error {
	c.env.mu.Lock()
	defer c.env.mu.Unlock()
	return c.env.config.SetSecret(path, value)
}

func (c *environmentConfig) Unset(path string) error {
	c.env.mu.Lock()
	defer c.env.mu.Unlock()
	return c.env.config.Unset(path)
}

func (c *environmentConfig) IsEmpty() bool {
	c.env.mu.RLock()
	defer c.env.mu.RUnlock()
	return c.env.config.IsEmpty()
}

func (c *environmentConfig) Clone() (config.Config, error) {
	c.env.mu.RLock()
	defer c.env.mu.RUnlock()
	return config.Clone(c.env.config)
}

func cloneConfigValue[T any](value T) T {
	cloned, err := config.CloneValue(value)
	if err != nil {
		// Writes are checked before they reach the environment, so a failure here
		// means we let an unsupported value into the stored state.
		panic(fmt.Errorf("invalid environment configuration state: %w", err))
	}
	return cloned
}
