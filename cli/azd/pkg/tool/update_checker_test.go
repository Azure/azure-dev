// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"context"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// mockUserConfigManager — in-package mock for config.UserConfigManager
// ---------------------------------------------------------------------------

type mockUserConfigManager struct {
	cfg config.Config
}

func newMockUserConfigManager() *mockUserConfigManager {
	return &mockUserConfigManager{cfg: config.NewEmptyConfig()}
}

func (m *mockUserConfigManager) Load() (config.Config, error) {
	return m.cfg, nil
}

func (m *mockUserConfigManager) Save(cfg config.Config) error {
	m.cfg = cfg
	return nil
}

// staticDir returns a configDirFn that always yields the given directory.
// This is a test helper for constructing [UpdateChecker] instances with
// a known, fixed directory.
func staticDir(dir string) func() (string, error) {
	return func() (string, error) { return dir, nil }
}

// ---------------------------------------------------------------------------
// ShouldCheck
// ---------------------------------------------------------------------------

func TestShouldCheck(t *testing.T) {
	t.Parallel()

	t.Run("FirstTimeReturnsTrue", func(t *testing.T) {
		t.Parallel()

		mgr := newMockUserConfigManager()
		uc := NewUpdateChecker(mgr, nil, staticDir(t.TempDir()))

		assert.True(t, uc.ShouldCheck(t.Context()))
	})

	t.Run("WithinIntervalReturnsFalse", func(t *testing.T) {
		t.Parallel()

		mgr := newMockUserConfigManager()
		// Set last check to 1 hour ago.
		err := mgr.cfg.Set(
			configKeyLastUpdateCheck,
			time.Now().Add(-1*time.Hour).UTC().Format(time.RFC3339),
		)
		require.NoError(t, err)

		uc := NewUpdateChecker(mgr, nil, staticDir(t.TempDir()))
		assert.False(t, uc.ShouldCheck(t.Context()))
	})

	t.Run("PastIntervalReturnsTrue", func(t *testing.T) {
		t.Parallel()

		mgr := newMockUserConfigManager()
		// Set last check to 200 hours ago (default interval is 168h).
		err := mgr.cfg.Set(
			configKeyLastUpdateCheck,
			time.Now().Add(-200*time.Hour).UTC().Format(time.RFC3339),
		)
		require.NoError(t, err)

		uc := NewUpdateChecker(mgr, nil, staticDir(t.TempDir()))
		assert.True(t, uc.ShouldCheck(t.Context()))
	})

	t.Run("UpdateChecksOffReturnsFalse", func(t *testing.T) {
		t.Parallel()

		mgr := newMockUserConfigManager()
		err := mgr.cfg.Set(configKeyUpdateChecks, "off")
		require.NoError(t, err)

		uc := NewUpdateChecker(mgr, nil, staticDir(t.TempDir()))
		assert.False(t, uc.ShouldCheck(t.Context()))
	})

	t.Run("InvalidTimestampReturnsTrue", func(t *testing.T) {
		t.Parallel()

		mgr := newMockUserConfigManager()
		err := mgr.cfg.Set(
			configKeyLastUpdateCheck, "not-a-timestamp",
		)
		require.NoError(t, err)

		uc := NewUpdateChecker(mgr, nil, staticDir(t.TempDir()))
		assert.True(t, uc.ShouldCheck(t.Context()))
	})

	t.Run("CustomIntervalHoursRespected", func(t *testing.T) {
		t.Parallel()

		mgr := newMockUserConfigManager()
		// Set a short 1-hour interval.
		err := mgr.cfg.Set(configKeyCheckIntervalHours, float64(1))
		require.NoError(t, err)

		// Last check was 2 hours ago.
		err = mgr.cfg.Set(
			configKeyLastUpdateCheck,
			time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339),
		)
		require.NoError(t, err)

		uc := NewUpdateChecker(mgr, nil, staticDir(t.TempDir()))
		assert.True(t, uc.ShouldCheck(t.Context()))
	})
}

// ---------------------------------------------------------------------------
// SaveCache / GetCachedResults round-trip
// ---------------------------------------------------------------------------

func TestSaveCacheAndGetCachedResults(t *testing.T) {
	t.Parallel()

	t.Run("RoundTrip", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		mgr := newMockUserConfigManager()
		uc := NewUpdateChecker(mgr, nil, staticDir(tmpDir))

		now := time.Now().UTC().Truncate(time.Second)
		cache := &UpdateCheckCache{
			CheckedAt: now,
			ExpiresAt: now.Add(168 * time.Hour),
			Tools: map[string]CachedToolVersion{
				"az-cli": {LatestVersion: "2.65.0"},
				"vscode-bicep": {
					LatestVersion: "0.30.0",
				},
			},
		}

		err := uc.SaveCache(cache)
		require.NoError(t, err)

		loaded, err := uc.GetCachedResults()
		require.NoError(t, err)
		require.NotNil(t, loaded)

		assert.Equal(t,
			cache.CheckedAt.Unix(),
			loaded.CheckedAt.Unix(),
		)
		assert.Len(t, loaded.Tools, 2)
		assert.Equal(t, "2.65.0",
			loaded.Tools["az-cli"].LatestVersion)
		assert.Equal(t, "0.30.0",
			loaded.Tools["vscode-bicep"].LatestVersion)
	})

	t.Run("NoCacheFileReturnsNilNil", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		mgr := newMockUserConfigManager()
		uc := NewUpdateChecker(mgr, nil, staticDir(tmpDir))

		cache, err := uc.GetCachedResults()
		assert.NoError(t, err)
		assert.Nil(t, cache)
	})

	t.Run("EmptyToolsMap", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		mgr := newMockUserConfigManager()
		uc := NewUpdateChecker(mgr, nil, staticDir(tmpDir))

		cache := &UpdateCheckCache{
			CheckedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(time.Hour),
			Tools:     map[string]CachedToolVersion{},
		}

		require.NoError(t, uc.SaveCache(cache))

		loaded, err := uc.GetCachedResults()
		require.NoError(t, err)
		require.NotNil(t, loaded)
		assert.Empty(t, loaded.Tools)
	})
}

// ---------------------------------------------------------------------------
// ShouldShowNotification
// ---------------------------------------------------------------------------

func TestShouldShowNotification(t *testing.T) {
	t.Parallel()

	t.Run("NoCheckPerformedReturnsFalse", func(t *testing.T) {
		t.Parallel()

		mgr := newMockUserConfigManager()
		uc := NewUpdateChecker(mgr, nil, staticDir(t.TempDir()))

		assert.False(t, uc.ShouldShowNotification(t.Context()))
	})

	t.Run("CheckExistsNoNotificationReturnsTrue", func(t *testing.T) {
		t.Parallel()

		mgr := newMockUserConfigManager()
		err := mgr.cfg.Set(
			configKeyLastUpdateCheck,
			time.Now().UTC().Format(time.RFC3339),
		)
		require.NoError(t, err)

		uc := NewUpdateChecker(mgr, nil, staticDir(t.TempDir()))
		assert.True(t, uc.ShouldShowNotification(t.Context()))
	})

	t.Run("NotificationAlreadyShownAfterCheckReturnsFalse",
		func(t *testing.T) {
			t.Parallel()

			mgr := newMockUserConfigManager()
			checkTime := time.Now().Add(-1 * time.Hour).UTC()
			shownTime := time.Now().UTC()

			err := mgr.cfg.Set(
				configKeyLastUpdateCheck,
				checkTime.Format(time.RFC3339),
			)
			require.NoError(t, err)
			err = mgr.cfg.Set(
				configKeyLastNotificationShown,
				shownTime.Format(time.RFC3339),
			)
			require.NoError(t, err)

			uc := NewUpdateChecker(mgr, nil, staticDir(t.TempDir()))
			assert.False(t,
				uc.ShouldShowNotification(t.Context()))
		},
	)

	t.Run("NotificationShownBeforeCheckReturnsTrue",
		func(t *testing.T) {
			t.Parallel()

			mgr := newMockUserConfigManager()
			shownTime := time.Now().Add(-2 * time.Hour).UTC()
			checkTime := time.Now().Add(-1 * time.Hour).UTC()

			err := mgr.cfg.Set(
				configKeyLastUpdateCheck,
				checkTime.Format(time.RFC3339),
			)
			require.NoError(t, err)
			err = mgr.cfg.Set(
				configKeyLastNotificationShown,
				shownTime.Format(time.RFC3339),
			)
			require.NoError(t, err)

			uc := NewUpdateChecker(mgr, nil, staticDir(t.TempDir()))
			assert.True(t,
				uc.ShouldShowNotification(t.Context()))
		},
	)
}

// ---------------------------------------------------------------------------
// MarkNotificationShown
// ---------------------------------------------------------------------------

func TestMarkNotificationShown(t *testing.T) {
	t.Parallel()

	mgr := newMockUserConfigManager()
	uc := NewUpdateChecker(mgr, nil, staticDir(t.TempDir()))

	err := uc.MarkNotificationShown(t.Context())
	require.NoError(t, err)

	// Verify the timestamp was persisted.
	val, ok := mgr.cfg.GetString(configKeyLastNotificationShown)
	require.True(t, ok, "expected lastNotificationShown to be set")

	ts, parseErr := time.Parse(time.RFC3339, val)
	require.NoError(t, parseErr)
	assert.WithinDuration(t, time.Now().UTC(), ts, 5*time.Second)
}

// ---------------------------------------------------------------------------
// loadIntervalHours helper
// ---------------------------------------------------------------------------

func TestLoadIntervalHours(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		value  any
		expect int
	}{
		{
			name:   "DefaultWhenUnset",
			value:  nil,
			expect: defaultCheckIntervalHours,
		},
		{
			name:   "Float64Value",
			value:  float64(24),
			expect: 24,
		},
		{
			name:   "IntValue",
			value:  48,
			expect: 48,
		},
		{
			name:   "StringValue",
			value:  "72",
			expect: 72,
		},
		{
			name:   "InvalidStringFallsBackToDefault",
			value:  "not-a-number",
			expect: defaultCheckIntervalHours,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.NewEmptyConfig()
			if tt.value != nil {
				err := cfg.Set(configKeyCheckIntervalHours, tt.value)
				require.NoError(t, err)
			}

			result := loadIntervalHours(cfg)
			assert.Equal(t, tt.expect, result)
		})
	}
}

// ---------------------------------------------------------------------------
// Check (integration-like test with mocks)
// ---------------------------------------------------------------------------

func TestCheck(t *testing.T) {
	t.Parallel()

	t.Run("DetectsToolsAndSavesCache", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		mgr := newMockUserConfigManager()

		det := &mockDetector{
			detectAllFn: func(
				_ context.Context, tools []*ToolDefinition,
			) ([]*ToolStatus, error) {
				results := make([]*ToolStatus, len(tools))
				for i, tool := range tools {
					results[i] = &ToolStatus{
						Tool:             tool,
						Installed:        true,
						InstalledVersion: "1.0.0",
					}
				}
				return results, nil
			},
		}

		uc := NewUpdateChecker(mgr, det, staticDir(tmpDir))

		tools := []*ToolDefinition{
			{Id: "tool-a", Name: "Tool A"},
			{Id: "tool-b", Name: "Tool B"},
		}

		results, err := uc.Check(t.Context(), tools)
		require.NoError(t, err)
		require.Len(t, results, 2)

		for _, r := range results {
			assert.Equal(t, "1.0.0", r.CurrentVersion)
		}

		// Verify cache was persisted.
		cache, cacheErr := uc.GetCachedResults()
		require.NoError(t, cacheErr)
		require.NotNil(t, cache)
		assert.Len(t, cache.Tools, 2)

		// Verify timestamp was recorded.
		val, ok := mgr.cfg.GetString(configKeyLastUpdateCheck)
		require.True(t, ok)
		_, parseErr := time.Parse(time.RFC3339, val)
		assert.NoError(t, parseErr)
	})

	t.Run("CarriesForwardCachedLatestVersion", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		mgr := newMockUserConfigManager()

		det := &mockDetector{
			detectAllFn: func(
				_ context.Context, tools []*ToolDefinition,
			) ([]*ToolStatus, error) {
				results := make([]*ToolStatus, len(tools))
				for i, tool := range tools {
					results[i] = &ToolStatus{
						Tool:             tool,
						Installed:        true,
						InstalledVersion: "1.0.0",
					}
				}
				return results, nil
			},
		}

		uc := NewUpdateChecker(mgr, det, staticDir(tmpDir))

		// Seed a cache with a known latest version.
		seedCache := &UpdateCheckCache{
			CheckedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(time.Hour),
			Tools: map[string]CachedToolVersion{
				"tool-a": {LatestVersion: "2.0.0"},
			},
		}
		require.NoError(t, uc.SaveCache(seedCache))

		tools := []*ToolDefinition{
			{Id: "tool-a", Name: "Tool A"},
		}

		results, err := uc.Check(t.Context(), tools)
		require.NoError(t, err)
		require.Len(t, results, 1)

		assert.Equal(t, "1.0.0", results[0].CurrentVersion)
		assert.Equal(t, "2.0.0", results[0].LatestVersion)
		assert.True(t, results[0].UpdateAvailable)
	})
}

// ---------------------------------------------------------------------------
// HasUpdatesAvailable
// ---------------------------------------------------------------------------

func TestHasUpdatesAvailable(t *testing.T) {
	t.Parallel()

	t.Run("NoCacheReturnsNoUpdates", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		mgr := newMockUserConfigManager()
		det := &mockDetector{}
		uc := NewUpdateChecker(mgr, det, staticDir(tmpDir))

		hasUpdates, count, err := uc.HasUpdatesAvailable(t.Context())
		require.NoError(t, err)
		assert.False(t, hasUpdates)
		assert.Equal(t, 0, count)
	})

	t.Run("EmptyCacheToolsReturnsNoUpdates", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		mgr := newMockUserConfigManager()
		det := &mockDetector{}
		uc := NewUpdateChecker(mgr, det, staticDir(tmpDir))

		cache := &UpdateCheckCache{
			CheckedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(time.Hour),
			Tools:     map[string]CachedToolVersion{},
		}
		require.NoError(t, uc.SaveCache(cache))

		hasUpdates, count, err := uc.HasUpdatesAvailable(t.Context())
		require.NoError(t, err)
		assert.False(t, hasUpdates)
		assert.Equal(t, 0, count)
	})
}
