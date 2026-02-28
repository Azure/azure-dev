// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Test_Integration_UpdateCheck_FullFlow tests the complete update checking flow:
// 1. Cache miss (no cache file) -> returns no update info
// 2. Cache populated -> can retrieve latest version
// 3. Version comparison -> detects update available
// 4. Warning cooldown -> respects 24-hour cooldown (stored in extension.LastUpdateWarning)
// 5. Per-source isolation -> sources don't interfere
func Test_Integration_UpdateCheck_FullFlow(t *testing.T) {
	// Create isolated test directories
	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache", "extensions")
	require.NoError(t, os.MkdirAll(cacheDir, 0755))

	// Create cache manager with test directory
	cacheManager := &RegistryCacheManager{
		cacheDir: cacheDir,
		ttl:      4 * time.Hour,
	}

	// Create update checker
	updateChecker := &UpdateChecker{
		cacheManager: cacheManager,
	}

	ctx := context.Background()

	// Step 1: Cache miss scenario
	t.Run("cache_miss_returns_no_update", func(t *testing.T) {
		extension := &Extension{
			Id:          "test.extension",
			DisplayName: "Test Extension",
			Version:     "1.0.0",
			Source:      "https://example.com/registry.json",
		}

		result, err := updateChecker.CheckForUpdate(ctx, extension)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.False(t, result.HasUpdate, "should not report update when cache is missing")
		require.Equal(t, "1.0.0", result.InstalledVersion)
		require.Empty(t, result.LatestVersion, "latest version should be empty on cache miss")
	})

	// Step 2: Populate cache and verify retrieval
	t.Run("cache_populated_enables_version_lookup", func(t *testing.T) {
		sourceName := "https://example.com/registry.json"
		extensions := []*ExtensionMetadata{
			{
				Id:          "test.extension",
				DisplayName: "Test Extension",
				Versions: []ExtensionVersion{
					{Version: "1.0.0"},
					{Version: "1.1.0"},
					{Version: "2.0.0"},
				},
			},
		}

		// Write cache
		err := cacheManager.Set(ctx, sourceName, extensions)
		require.NoError(t, err)

		// Verify cache file was created
		cacheFilePath := cacheManager.getCacheFilePath(sourceName)
		require.FileExists(t, cacheFilePath)

		// Verify we can retrieve the latest version
		latestVersion, err := cacheManager.GetExtensionLatestVersion(ctx, sourceName, "test.extension")
		require.NoError(t, err)
		require.Equal(t, "2.0.0", latestVersion)
	})

	// Step 3: Version comparison detects update
	t.Run("version_comparison_detects_update", func(t *testing.T) {
		extension := &Extension{
			Id:          "test.extension",
			DisplayName: "Test Extension",
			Version:     "1.0.0",
			Source:      "https://example.com/registry.json",
		}

		result, err := updateChecker.CheckForUpdate(ctx, extension)
		require.NoError(t, err)
		require.True(t, result.HasUpdate, "should detect update from 1.0.0 to 2.0.0")
		require.Equal(t, "1.0.0", result.InstalledVersion)
		require.Equal(t, "2.0.0", result.LatestVersion)
		require.Equal(t, "test.extension", result.ExtensionId)
		require.Equal(t, "Test Extension", result.ExtensionName)
	})

	// Step 4: No update when already on latest
	t.Run("no_update_when_on_latest", func(t *testing.T) {
		extension := &Extension{
			Id:          "test.extension",
			DisplayName: "Test Extension",
			Version:     "2.0.0",
			Source:      "https://example.com/registry.json",
		}

		result, err := updateChecker.CheckForUpdate(ctx, extension)
		require.NoError(t, err)
		require.False(t, result.HasUpdate, "should not report update when on latest")
		require.Equal(t, "2.0.0", result.InstalledVersion)
		require.Equal(t, "2.0.0", result.LatestVersion)
	})

	// Step 5: Warning cooldown tracking (now stored in extension struct)
	t.Run("warning_cooldown_is_respected", func(t *testing.T) {
		extension := &Extension{
			Id:          "test.extension",
			DisplayName: "Test Extension",
			Version:     "1.0.0",
			Source:      "https://example.com/registry.json",
		}

		// First time - should show warning (no LastUpdateWarning set)
		require.True(t, updateChecker.ShouldShowWarning(extension))

		// Record warning (updates extension's LastUpdateWarning field)
		RecordUpdateWarningShown(extension)
		require.NotEmpty(t, extension.LastUpdateWarning)

		// Immediately after - should not show warning (cooldown)
		require.False(t, updateChecker.ShouldShowWarning(extension))
	})

	// Step 6: Different extension should still show warning
	t.Run("different_extension_shows_warning", func(t *testing.T) {
		otherExtension := &Extension{
			Id:          "other.extension",
			DisplayName: "Other Extension",
			Version:     "1.0.0",
			Source:      "https://example.com/registry.json",
		}
		// Different extension should not be affected by cooldown
		require.True(t, updateChecker.ShouldShowWarning(otherExtension))
	})

	// Step 7: Verify warning format
	t.Run("warning_message_format", func(t *testing.T) {
		result := &UpdateCheckResult{
			ExtensionId:      "test.extension",
			ExtensionName:    "Test Extension",
			InstalledVersion: "1.0.0",
			LatestVersion:    "2.0.0",
			HasUpdate:        true,
		}

		warning := FormatUpdateWarning(result)
		require.NotNil(t, warning)
		require.Contains(t, warning.Description, "Test Extension")
		require.Contains(t, warning.Description, "1.0.0")
		require.Contains(t, warning.Description, "2.0.0")
		require.False(t, warning.HidePrefix)
		require.Len(t, warning.Hints, 2)
		require.Contains(t, warning.Hints[0], "azd extension upgrade --all")
		require.Contains(t, warning.Hints[0], "azd extension upgrade test.extension")
		require.Contains(t, warning.Hints[1], "azd extension uninstall test.extension")
	})
}

// Test_Integration_PerSourceIsolation tests that per-source caching works correctly
func Test_Integration_PerSourceIsolation(t *testing.T) {
	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache", "extensions")
	require.NoError(t, os.MkdirAll(cacheDir, 0755))

	cacheManager := &RegistryCacheManager{
		cacheDir: cacheDir,
		ttl:      4 * time.Hour,
	}

	ctx := context.Background()

	// Create two different sources
	source1 := "https://registry1.example.com/registry.json"
	source2 := "https://registry2.example.com/registry.json"

	// Populate source1 with version 2.0.0
	err := cacheManager.Set(ctx, source1, []*ExtensionMetadata{
		{
			Id:       "shared.extension",
			Versions: []ExtensionVersion{{Version: "2.0.0"}},
		},
	})
	require.NoError(t, err)

	// Populate source2 with version 3.0.0 (different version for same extension ID)
	err = cacheManager.Set(ctx, source2, []*ExtensionMetadata{
		{
			Id:       "shared.extension",
			Versions: []ExtensionVersion{{Version: "3.0.0"}},
		},
	})
	require.NoError(t, err)

	// Verify isolation - each source returns its own version
	ver1, err := cacheManager.GetExtensionLatestVersion(ctx, source1, "shared.extension")
	require.NoError(t, err)
	require.Equal(t, "2.0.0", ver1)

	ver2, err := cacheManager.GetExtensionLatestVersion(ctx, source2, "shared.extension")
	require.NoError(t, err)
	require.Equal(t, "3.0.0", ver2)

	// Verify separate cache files exist
	require.FileExists(t, cacheManager.getCacheFilePath(source1))
	require.FileExists(t, cacheManager.getCacheFilePath(source2))

	// Verify they are different files
	require.NotEqual(t,
		cacheManager.getCacheFilePath(source1),
		cacheManager.getCacheFilePath(source2),
	)
}

// Test_Integration_CacheExpiration tests cache expiration behavior
func Test_Integration_CacheExpiration(t *testing.T) {
	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache", "extensions")
	require.NoError(t, os.MkdirAll(cacheDir, 0755))

	// Create manager with very short TTL for testing
	cacheManager := &RegistryCacheManager{
		cacheDir: cacheDir,
		ttl:      1 * time.Millisecond,
	}

	ctx := context.Background()
	sourceName := "https://example.com/registry.json"

	// Write cache
	err := cacheManager.Set(ctx, sourceName, []*ExtensionMetadata{
		{
			Id:       "test.extension",
			Versions: []ExtensionVersion{{Version: "1.0.0"}},
		},
	})
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	// Cache should be expired
	require.True(t, cacheManager.IsExpiredOrMissing(ctx, sourceName))

	// Get should return error
	_, err = cacheManager.Get(ctx, sourceName)
	require.ErrorIs(t, err, ErrCacheExpired)
}

// Test_Integration_WarningCooldownExpiration tests cooldown expiration
func Test_Integration_WarningCooldownExpiration(t *testing.T) {
	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache", "extensions")
	require.NoError(t, os.MkdirAll(cacheDir, 0755))

	cacheManager := &RegistryCacheManager{
		cacheDir: cacheDir,
		ttl:      4 * time.Hour,
	}

	updateChecker := &UpdateChecker{
		cacheManager: cacheManager,
	}

	// Extension with old warning timestamp (25 hours ago)
	extension := &Extension{
		Id:                "test.extension",
		DisplayName:       "Test Extension",
		Version:           "1.0.0",
		Source:            "https://example.com/registry.json",
		LastUpdateWarning: time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339),
	}

	// Should show warning since cooldown expired
	require.True(t, updateChecker.ShouldShowWarning(extension))
}

// Test_Integration_NetworkFailureGraceful tests graceful handling when cache is stale
func Test_Integration_NetworkFailureGraceful(t *testing.T) {
	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache", "extensions")
	require.NoError(t, os.MkdirAll(cacheDir, 0755))

	cacheManager := &RegistryCacheManager{
		cacheDir: cacheDir,
		ttl:      4 * time.Hour,
	}

	updateChecker := &UpdateChecker{
		cacheManager: cacheManager,
	}

	ctx := context.Background()

	// Extension with source that has no cache (simulates network failure)
	extension := &Extension{
		Id:          "test.extension",
		DisplayName: "Test Extension",
		Version:     "1.0.0",
		Source:      "https://unreachable.example.com/registry.json",
	}

	// Should not error, just return no update available
	result, err := updateChecker.CheckForUpdate(ctx, extension)
	require.NoError(t, err, "should not error on cache miss")
	require.NotNil(t, result)
	require.False(t, result.HasUpdate, "should not report update when cache unavailable")
	require.Equal(t, "1.0.0", result.InstalledVersion)
	require.Empty(t, result.LatestVersion)
}
