// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newManagerWithSources creates a Manager with pre-loaded mock sources for testing.
func newManagerWithSources(sources ...Source) *Manager {
	return &Manager{
		sources: sources,
	}
}

// Test_Integration_UpdateCheck_FullFlow tests the complete update checking flow:
// 1. Extension not found -> returns no update info
// 2. Extension found in registry -> can retrieve latest version
// 3. Version comparison -> detects update available
// 4. Warning cooldown -> respects 24-hour cooldown (stored in extension.LastUpdateWarning)
// 5. Per-source isolation -> sources don't interfere
func Test_Integration_UpdateCheck_FullFlow(t *testing.T) {
	ctx := context.Background()

	source := &mockSource{
		name: "https://example.com/registry.json",
		extensions: []*ExtensionMetadata{
			{
				Id:          "test.extension",
				DisplayName: "Test Extension",
				Source:      "https://example.com/registry.json",
				Versions: []ExtensionVersion{
					{Version: "1.0.0"},
					{Version: "1.1.0"},
					{Version: "2.0.0"},
				},
			},
		},
	}

	// Step 1: Extension not found scenario (empty source)
	t.Run("extension_not_found_returns_no_update", func(t *testing.T) {
		manager := newManagerWithSources(&mockSource{name: "https://example.com/registry.json"})
		updateChecker := NewUpdateChecker(manager)

		extension := &Extension{
			Id:          "test.extension",
			DisplayName: "Test Extension",
			Version:     "1.0.0",
			Source:      "https://example.com/registry.json",
		}

		result, err := updateChecker.CheckForUpdate(ctx, extension)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.False(t, result.HasUpdate, "should not report update when extension not found")
		require.Equal(t, "1.0.0", result.InstalledVersion)
		require.Empty(t, result.LatestVersion, "latest version should be empty when not found")
	})

	// Step 2: Extension found in registry - verify retrieval
	t.Run("extension_found_returns_latest_version", func(t *testing.T) {
		manager := newManagerWithSources(source)
		latestVersion, err := manager.GetLatestVersion(ctx, "https://example.com/registry.json", "test.extension")
		require.NoError(t, err)
		require.Equal(t, "2.0.0", latestVersion)
	})

	// Step 3: Version comparison detects update
	t.Run("version_comparison_detects_update", func(t *testing.T) {
		manager := newManagerWithSources(source)
		updateChecker := NewUpdateChecker(manager)

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
		manager := newManagerWithSources(source)
		updateChecker := NewUpdateChecker(manager)

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

	// Step 5: Warning cooldown tracking (stored in extension struct)
	t.Run("warning_cooldown_is_respected", func(t *testing.T) {
		manager := newManagerWithSources(source)
		updateChecker := NewUpdateChecker(manager)

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
		manager := newManagerWithSources(source)
		updateChecker := NewUpdateChecker(manager)

		otherExtension := &Extension{
			Id:          "other.extension",
			DisplayName: "Other Extension",
			Version:     "1.0.0",
			Source:      "https://example.com/registry.json",
		}
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
		require.Contains(t, warning.Hints[0], "azd extension upgrade test.extension")
		require.Contains(t, warning.Hints[1], "azd extension upgrade --all")
	})
}

// Test_Integration_PerSourceIsolation tests that per-source version lookup works correctly
func Test_Integration_PerSourceIsolation(t *testing.T) {
	ctx := context.Background()

	source1 := &mockSource{
		name: "https://registry1.example.com/registry.json",
		extensions: []*ExtensionMetadata{
			{
				Id:     "shared.extension",
				Source: "https://registry1.example.com/registry.json",
				Versions: []ExtensionVersion{
					{Version: "2.0.0"},
				},
			},
		},
	}

	source2 := &mockSource{
		name: "https://registry2.example.com/registry.json",
		extensions: []*ExtensionMetadata{
			{
				Id:     "shared.extension",
				Source: "https://registry2.example.com/registry.json",
				Versions: []ExtensionVersion{
					{Version: "3.0.0"},
				},
			},
		},
	}

	manager := newManagerWithSources(source1, source2)

	// Verify isolation - each source returns its own version
	ver1, err := manager.GetLatestVersion(ctx, "https://registry1.example.com/registry.json", "shared.extension")
	require.NoError(t, err)
	require.Equal(t, "2.0.0", ver1)

	ver2, err := manager.GetLatestVersion(ctx, "https://registry2.example.com/registry.json", "shared.extension")
	require.NoError(t, err)
	require.Equal(t, "3.0.0", ver2)
}

// Test_Integration_WarningCooldownExpiration tests cooldown expiration
func Test_Integration_WarningCooldownExpiration(t *testing.T) {
	manager := newManagerWithSources()
	updateChecker := NewUpdateChecker(manager)

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

// Test_Integration_NetworkFailureGraceful tests graceful handling when extension not in registry
func Test_Integration_NetworkFailureGraceful(t *testing.T) {
	ctx := context.Background()

	// Manager with empty source (simulates extension not found / network failure)
	manager := newManagerWithSources(&mockSource{name: "https://unreachable.example.com/registry.json"})
	updateChecker := NewUpdateChecker(manager)

	extension := &Extension{
		Id:          "test.extension",
		DisplayName: "Test Extension",
		Version:     "1.0.0",
		Source:      "https://unreachable.example.com/registry.json",
	}

	// Should not error, just return no update available
	result, err := updateChecker.CheckForUpdate(ctx, extension)
	require.NoError(t, err, "should not error when extension not found")
	require.NotNil(t, result)
	require.False(t, result.HasUpdate, "should not report update when extension unavailable")
	require.Equal(t, "1.0.0", result.InstalledVersion)
	require.Empty(t, result.LatestVersion)
}

