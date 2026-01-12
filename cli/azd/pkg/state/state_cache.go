// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StateCache represents cached Azure resource information for an environment
type StateCache struct {
	// Version of the cache format
	Version int `json:"version"`
	// Timestamp when the cache was last updated
	UpdatedAt time.Time `json:"updatedAt"`
	// Subscription ID
	SubscriptionId string `json:"subscriptionId,omitempty"`
	// Resource group name
	ResourceGroupName string `json:"resourceGroupName,omitempty"`
	// Service resources mapped by service name
	ServiceResources map[string]ServiceResourceCache `json:"serviceResources,omitempty"`
}

// ServiceResourceCache represents cached resource information for a service
type ServiceResourceCache struct {
	// Resource IDs associated with this service
	ResourceIds []string `json:"resourceIds,omitempty"`
	// Ingress URL for the service
	IngressUrl string `json:"ingressUrl,omitempty"`
}

const (
	StateCacheVersion       = 1
	StateCacheFileName      = ".state.json"
	StateChangeFileName     = ".state-change"
	DefaultCacheTTLDuration = 24 * time.Hour
)

// StateCacheManager manages the state cache for environments
type StateCacheManager struct {
	rootPath string
	ttl      time.Duration
}

// NewStateCacheManager creates a new state cache manager
func NewStateCacheManager(rootPath string) *StateCacheManager {
	return &StateCacheManager{
		rootPath: rootPath,
		ttl:      DefaultCacheTTLDuration,
	}
}

// SetTTL sets the time-to-live for cached data
func (m *StateCacheManager) SetTTL(ttl time.Duration) {
	m.ttl = ttl
}

// GetCachePath returns the path to the cache file for an environment
func (m *StateCacheManager) GetCachePath(envName string) string {
	return filepath.Join(m.rootPath, envName, StateCacheFileName)
}

// GetStateChangePath returns the path to the state change notification file
func (m *StateCacheManager) GetStateChangePath() string {
	return filepath.Join(m.rootPath, StateChangeFileName)
}

// Load loads the state cache for an environment
func (m *StateCacheManager) Load(ctx context.Context, envName string) (*StateCache, error) {
	// Check for context cancellation
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cachePath := m.GetCachePath(envName)

	data, err := os.ReadFile(cachePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil // Cache doesn't exist, not an error
		}
		return nil, fmt.Errorf("reading cache file: %w", err)
	}

	var cache StateCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("parsing cache file: %w", err)
	}

	// Check if cache is expired
	if m.ttl > 0 && time.Since(cache.UpdatedAt) > m.ttl {
		return nil, nil // Cache is expired, treat as if it doesn't exist
	}

	return &cache, nil
}

// Save saves the state cache for an environment
func (m *StateCacheManager) Save(ctx context.Context, envName string, cache *StateCache) error {
	// Check for context cancellation
	if err := ctx.Err(); err != nil {
		return err
	}

	cache.Version = StateCacheVersion
	cache.UpdatedAt = time.Now()

	cachePath := m.GetCachePath(envName)

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing cache: %w", err)
	}

	if err := os.WriteFile(cachePath, data, 0600); err != nil {
		return fmt.Errorf("writing cache file: %w", err)
	}

	// Check for context cancellation before updating state change file
	if err := ctx.Err(); err != nil {
		return err
	}

	// Update the state change notification file
	if err := m.TouchStateChange(); err != nil {
		return fmt.Errorf("updating state change file: %w", err)
	}

	return nil
}

// Invalidate removes the cache for an environment
func (m *StateCacheManager) Invalidate(ctx context.Context, envName string) error {
	// Check for context cancellation
	if err := ctx.Err(); err != nil {
		return err
	}

	cachePath := m.GetCachePath(envName)

	err := os.Remove(cachePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing cache file: %w", err)
	}

	// Check for context cancellation before updating state change file
	if err := ctx.Err(); err != nil {
		return err
	}

	// Update the state change notification file
	if err := m.TouchStateChange(); err != nil {
		return fmt.Errorf("updating state change file: %w", err)
	}

	return nil
}

// TouchStateChange updates the state change notification file
// This file is watched by IDEs/tools to know when to refresh their state
func (m *StateCacheManager) TouchStateChange() error {
	stateChangePath := m.GetStateChangePath()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(stateChangePath), 0755); err != nil {
		return fmt.Errorf("creating state change directory: %w", err)
	}

	// Write current timestamp to the file
	timestamp := time.Now().Format(time.RFC3339)
	if err := os.WriteFile(stateChangePath, []byte(timestamp), 0600); err != nil {
		return fmt.Errorf("writing state change file: %w", err)
	}

	return nil
}

// GetStateChangeTime returns the last time the state changed
func (m *StateCacheManager) GetStateChangeTime() (time.Time, error) {
	stateChangePath := m.GetStateChangePath()

	data, err := os.ReadFile(stateChangePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("reading state change file: %w", err)
	}

	timestamp, err := time.Parse(time.RFC3339, string(data))
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing timestamp: %w", err)
	}

	return timestamp, nil
}
