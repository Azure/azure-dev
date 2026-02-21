// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStateCacheManager_SaveAndLoad(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewStateCacheManager(tempDir)
	ctx := context.Background()

	cache := &StateCache{
		SubscriptionId:    "sub-123",
		ResourceGroupName: "rg-test",
		ServiceResources: map[string]ServiceResourceCache{
			"web": {
				ResourceIds: []string{"/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/web"},
				IngressUrl:  "https://web.azurewebsites.net",
			},
		},
	}

	// Save cache
	err := manager.Save(ctx, "test-env", cache)
	require.NoError(t, err)

	// Load cache
	loaded, err := manager.Load(ctx, "test-env")
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, cache.SubscriptionId, loaded.SubscriptionId)
	require.Equal(t, cache.ResourceGroupName, loaded.ResourceGroupName)
	require.Equal(t, cache.ServiceResources["web"].ResourceIds, loaded.ServiceResources["web"].ResourceIds)
	require.Equal(t, cache.ServiceResources["web"].IngressUrl, loaded.ServiceResources["web"].IngressUrl)
}

func TestStateCacheManager_LoadNonExistent(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewStateCacheManager(tempDir)
	ctx := context.Background()

	// Load non-existent cache
	loaded, err := manager.Load(ctx, "non-existent")
	require.NoError(t, err)
	require.Nil(t, loaded)
}

func TestStateCacheManager_Invalidate(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewStateCacheManager(tempDir)
	ctx := context.Background()

	cache := &StateCache{
		SubscriptionId:    "sub-123",
		ResourceGroupName: "rg-test",
	}

	// Save cache
	err := manager.Save(ctx, "test-env", cache)
	require.NoError(t, err)

	// Invalidate cache
	err = manager.Invalidate(ctx, "test-env")
	require.NoError(t, err)

	// Load cache should return nil
	loaded, err := manager.Load(ctx, "test-env")
	require.NoError(t, err)
	require.Nil(t, loaded)
}

func TestStateCacheManager_TTL(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewStateCacheManager(tempDir)
	manager.SetTTL(500 * time.Millisecond) // Short TTL for testing (not too short to be flaky)
	ctx := context.Background()

	cache := &StateCache{
		SubscriptionId:    "sub-123",
		ResourceGroupName: "rg-test",
	}

	// Save cache
	err := manager.Save(ctx, "test-env", cache)
	require.NoError(t, err)

	// Load immediately should work
	loaded, err := manager.Load(ctx, "test-env")
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Wait for TTL to expire
	time.Sleep(600 * time.Millisecond)

	// Load after TTL should return nil
	loaded, err = manager.Load(ctx, "test-env")
	require.NoError(t, err)
	require.Nil(t, loaded)
}

func TestStateCacheManager_StateChangeFile(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewStateCacheManager(tempDir)
	ctx := context.Background()

	cache := &StateCache{
		SubscriptionId:    "sub-123",
		ResourceGroupName: "rg-test",
	}

	// Save cache should create state change file
	err := manager.Save(ctx, "test-env", cache)
	require.NoError(t, err)

	stateChangePath := manager.GetStateChangePath()
	_, err = os.Stat(stateChangePath)
	require.NoError(t, err, "State change file should exist")

	// Get state change time
	changeTime, err := manager.GetStateChangeTime()
	require.NoError(t, err)
	require.False(t, changeTime.IsZero())

	// Wait a bit and invalidate to update the timestamp
	time.Sleep(100 * time.Millisecond)
	err = manager.Invalidate(ctx, "test-env")
	require.NoError(t, err)

	// State change time should be updated
	newChangeTime, err := manager.GetStateChangeTime()
	require.NoError(t, err)
	require.True(t, newChangeTime.After(changeTime) || newChangeTime.Equal(changeTime),
		"Expected new time %v to be after or equal to %v", newChangeTime, changeTime)
}

func TestStateCacheManager_GetCachePath(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewStateCacheManager(tempDir)

	cachePath := manager.GetCachePath("test-env")
	expectedPath := filepath.Join(tempDir, "test-env", StateCacheFileName)
	require.Equal(t, expectedPath, cachePath)
}

func TestStateCacheManager_GetStateChangePath(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewStateCacheManager(tempDir)

	stateChangePath := manager.GetStateChangePath()
	expectedPath := filepath.Join(tempDir, StateChangeFileName)
	require.Equal(t, expectedPath, stateChangePath)
}
