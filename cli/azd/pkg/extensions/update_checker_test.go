// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_UpdateChecker_CheckForUpdate(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	cacheManager, err := NewRegistryCacheManager()
	require.NoError(t, err)

	ctx := context.Background()
	sourceName := "test-source"

	// Set cache with extension version 2.0.0
	extensions := []*ExtensionMetadata{
		{
			Id:          "test.extension",
			DisplayName: "Test Extension",
			Versions: []ExtensionVersion{
				{Version: "1.0.0"},
				{Version: "2.0.0"},
			},
		},
	}
	err = cacheManager.Set(ctx, sourceName, extensions)
	require.NoError(t, err)

	updateChecker := NewUpdateChecker(cacheManager)

	// Test with older installed version
	extension := &Extension{
		Id:          "test.extension",
		DisplayName: "Test Extension",
		Version:     "1.0.0",
		Source:      sourceName,
	}

	result, err := updateChecker.CheckForUpdate(ctx, extension)
	require.NoError(t, err)
	require.True(t, result.HasUpdate)
	require.Equal(t, "1.0.0", result.InstalledVersion)
	require.Equal(t, "2.0.0", result.LatestVersion)

	// Test with same version
	extension.Version = "2.0.0"
	result, err = updateChecker.CheckForUpdate(ctx, extension)
	require.NoError(t, err)
	require.False(t, result.HasUpdate)

	// Test with newer installed version (shouldn't happen but should handle)
	extension.Version = "3.0.0"
	result, err = updateChecker.CheckForUpdate(ctx, extension)
	require.NoError(t, err)
	require.False(t, result.HasUpdate)
}

func Test_UpdateChecker_CheckForUpdate_CacheMiss(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	cacheManager, err := NewRegistryCacheManager()
	require.NoError(t, err)

	updateChecker := NewUpdateChecker(cacheManager)

	ctx := context.Background()

	// Extension from source with no cache
	extension := &Extension{
		Id:          "test.extension",
		DisplayName: "Test Extension",
		Version:     "1.0.0",
		Source:      "nonexistent-source",
	}

	result, err := updateChecker.CheckForUpdate(ctx, extension)
	require.NoError(t, err)
	require.False(t, result.HasUpdate)
}

func Test_UpdateChecker_WarningCooldown(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	cacheManager, err := NewRegistryCacheManager()
	require.NoError(t, err)

	updateChecker := NewUpdateChecker(cacheManager)

	extension := &Extension{
		Id:          "test.extension",
		DisplayName: "Test Extension",
		Version:     "1.0.0",
		Source:      "test-source",
	}

	// Initially should show warning (no LastUpdateWarning set)
	require.True(t, updateChecker.ShouldShowWarning(extension))

	// Record warning shown (updates extension's LastUpdateWarning)
	RecordUpdateWarningShown(extension)
	require.NotEmpty(t, extension.LastUpdateWarning)

	// Should not show warning again (within cooldown)
	require.False(t, updateChecker.ShouldShowWarning(extension))
}

func Test_UpdateChecker_WarningCooldown_Expired(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	cacheManager, err := NewRegistryCacheManager()
	require.NoError(t, err)

	updateChecker := NewUpdateChecker(cacheManager)

	// Extension with old warning timestamp (25 hours ago)
	extension := &Extension{
		Id:                "test.extension",
		DisplayName:       "Test Extension",
		Version:           "1.0.0",
		Source:            "test-source",
		LastUpdateWarning: time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339),
	}

	// Should show warning (cool down expired)
	require.True(t, updateChecker.ShouldShowWarning(extension))
}

func Test_FormatUpdateWarning(t *testing.T) {
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
}

func Test_FormatUpdateWarning_NoDisplayName(t *testing.T) {
	result := &UpdateCheckResult{
		ExtensionId:      "test.extension",
		ExtensionName:    "",
		InstalledVersion: "1.0.0",
		LatestVersion:    "2.0.0",
		HasUpdate:        true,
	}

	warning := FormatUpdateWarning(result)

	// Should fall back to extension ID
	require.Contains(t, warning.Description, "test.extension")
}

func Test_UpdateChecker_PrereleaseVersions(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	cacheManager, err := NewRegistryCacheManager()
	require.NoError(t, err)

	ctx := context.Background()
	sourceName := "test-source"

	// Set cache with prerelease version
	extensions := []*ExtensionMetadata{
		{
			Id:          "test.extension",
			DisplayName: "Test Extension",
			Versions: []ExtensionVersion{
				{Version: "1.0.0"},
				{Version: "2.0.0-beta.1"},
			},
		},
	}
	err = cacheManager.Set(ctx, sourceName, extensions)
	require.NoError(t, err)

	updateChecker := NewUpdateChecker(cacheManager)

	// Installed stable version should see prerelease as update
	extension := &Extension{
		Id:          "test.extension",
		DisplayName: "Test Extension",
		Version:     "1.0.0",
		Source:      sourceName,
	}

	result, err := updateChecker.CheckForUpdate(ctx, extension)
	require.NoError(t, err)
	// semver: 2.0.0-beta.1 is considered less than 2.0.0 but greater than 1.0.0
	require.True(t, result.HasUpdate)
}

func Test_UpdateChecker_InvalidVersions(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	cacheManager, err := NewRegistryCacheManager()
	require.NoError(t, err)

	ctx := context.Background()
	sourceName := "test-source"

	// Set cache with valid version
	extensions := []*ExtensionMetadata{
		{
			Id:          "test.extension",
			DisplayName: "Test Extension",
			Versions: []ExtensionVersion{
				{Version: "1.0.0"},
			},
		},
	}
	err = cacheManager.Set(ctx, sourceName, extensions)
	require.NoError(t, err)

	updateChecker := NewUpdateChecker(cacheManager)

	// Extension with invalid version string
	extension := &Extension{
		Id:          "test.extension",
		DisplayName: "Test Extension",
		Version:     "not-a-version",
		Source:      sourceName,
	}

	result, err := updateChecker.CheckForUpdate(ctx, extension)
	require.NoError(t, err)
	require.False(t, result.HasUpdate) // Should gracefully handle invalid version
}
