// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_UpdateChecker_CheckForUpdate(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	cacheManager, err := NewRegistryCacheManager()
	require.NoError(t, err)

	ctx := t.Context()
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

	ctx := t.Context()

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
	require.Contains(t, warning.Hints[0], "azd extension upgrade test.extension")
	require.Contains(t, warning.Hints[1], "azd extension upgrade --all")
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

	ctx := t.Context()
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

	ctx := t.Context()
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

// Test_UpdateChecker_NightlyVersions verifies that the update checker correctly
// orders the nightly prerelease versions produced by Set-ExtensionVersionVariable.ps1.
//
// Nightly versions built from a base without an existing prerelease take the shape:
//
//	<base>-nightly.<yyyyMMdd>.<buildId>   e.g. 0.1.0-nightly.20260618.1234
//
// Several transitions must hold for the upgrade experience to work:
//   - nightly -> stable: once the stable <base> ships it must supersede any nightly
//     built from that base (semver: a release outranks its prereleases).
//   - nightly -> next nightly: a later nightly (newer date, or same date with a
//     higher build id) must supersede an earlier one, and an earlier nightly must
//     never be offered as an update over a newer one.
//   - nightly vs. milestone prereleases: for a clean base, semver orders
//     alpha < beta < nightly < rc < stable, so a nightly supersedes alpha/beta of the
//     same base but is itself superseded by rc and the final release. When the base
//     already carries a prerelease label (e.g. 0.1.0-beta), the appended ".nightly"
//     segment outranks every numbered beta of that base, so only the final release
//     (or a higher base) supersedes it.
func Test_UpdateChecker_NightlyVersions(t *testing.T) {
	tests := []struct {
		name       string
		installed  string
		available  []string
		wantUpdate bool
	}{
		{
			name:       "nightly to stable",
			installed:  "0.1.0-nightly.20260618.1234",
			available:  []string{"0.1.0-nightly.20260618.1234", "0.1.0"},
			wantUpdate: true,
		},
		{
			name:       "nightly to next nightly newer date",
			installed:  "0.1.0-nightly.20260618.1234",
			available:  []string{"0.1.0-nightly.20260618.1234", "0.1.0-nightly.20260619.1234"},
			wantUpdate: true,
		},
		{
			name:       "nightly to next nightly same date higher build",
			installed:  "0.1.0-nightly.20260618.1234",
			available:  []string{"0.1.0-nightly.20260618.1234", "0.1.0-nightly.20260618.5678"},
			wantUpdate: true,
		},
		{
			name:       "same nightly is not an update",
			installed:  "0.1.0-nightly.20260618.1234",
			available:  []string{"0.1.0-nightly.20260618.1234"},
			wantUpdate: false,
		},
		{
			name:       "older nightly is not offered over newer installed",
			installed:  "0.1.0-nightly.20260619.1234",
			available:  []string{"0.1.0-nightly.20260618.1234"},
			wantUpdate: false,
		},
		{
			// Clean base: nightly outranks beta of the same base (b < n).
			name:       "nightly supersedes beta of same base",
			installed:  "0.1.0-beta.1",
			available:  []string{"0.1.0-beta.1", "0.1.0-nightly.20260618.1234"},
			wantUpdate: true,
		},
		{
			// Clean base: an older beta must never be offered over an installed nightly.
			name:       "beta is not offered over installed nightly",
			installed:  "0.1.0-nightly.20260618.1234",
			available:  []string{"0.1.0-beta.5", "0.1.0-nightly.20260618.1234"},
			wantUpdate: false,
		},
		{
			// Clean base: rc outranks nightly of the same base (n < r).
			name:       "rc supersedes nightly of same base",
			installed:  "0.1.0-nightly.20260618.1234",
			available:  []string{"0.1.0-nightly.20260618.1234", "0.1.0-rc.1"},
			wantUpdate: true,
		},
		{
			// Prerelease base gotcha: "0.1.0-beta.nightly..." outranks every numbered
			// beta of that base because numeric identifiers lose to alphanumeric ones,
			// so a later beta.N is NOT offered as an update.
			name:       "numbered beta does not supersede beta-base nightly",
			installed:  "0.1.0-beta.nightly.20260618.1234",
			available:  []string{"0.1.0-beta.2", "0.1.0-beta.nightly.20260618.1234"},
			wantUpdate: false,
		},
		{
			// Prerelease base: only the final stable release supersedes the beta-base nightly.
			name:       "stable supersedes beta-base nightly",
			installed:  "0.1.0-beta.nightly.20260618.1234",
			available:  []string{"0.1.0-beta.nightly.20260618.1234", "0.1.0"},
			wantUpdate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			t.Setenv("AZD_CONFIG_DIR", tempDir)

			cacheManager, err := NewRegistryCacheManager()
			require.NoError(t, err)

			ctx := t.Context()
			sourceName := "test-source"

			versions := make([]ExtensionVersion, 0, len(tt.available))
			for _, v := range tt.available {
				versions = append(versions, ExtensionVersion{Version: v})
			}

			err = cacheManager.Set(ctx, sourceName, []*ExtensionMetadata{
				{
					Id:          "test.extension",
					DisplayName: "Test Extension",
					Versions:    versions,
				},
			})
			require.NoError(t, err)

			updateChecker := NewUpdateChecker(cacheManager)

			extension := &Extension{
				Id:          "test.extension",
				DisplayName: "Test Extension",
				Version:     tt.installed,
				Source:      sourceName,
			}

			result, err := updateChecker.CheckForUpdate(ctx, extension)
			require.NoError(t, err)
			require.Equal(t, tt.wantUpdate, result.HasUpdate)
		})
	}
}
