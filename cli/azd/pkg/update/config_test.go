// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestParseChannel(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Channel
		wantErr bool
	}{
		{"stable", "stable", ChannelStable, false},
		{"daily", "daily", ChannelDaily, false},
		{"invalid", "nightly", "", true},
		{"empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseChannel(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestLoadUpdateConfig(t *testing.T) {
	tests := []struct {
		name     string
		config   map[string]any
		expected *UpdateConfig
	}{
		{
			name:   "defaults",
			config: map[string]any{},
			expected: &UpdateConfig{
				Channel: ChannelStable,
			},
		},
		{
			name: "daily channel with auto-update",
			config: map[string]any{
				"updates": map[string]any{
					"channel":    "daily",
					"autoUpdate": "on",
				},
			},
			expected: &UpdateConfig{
				Channel:    ChannelDaily,
				AutoUpdate: true,
			},
		},
		{
			name: "custom check interval",
			config: map[string]any{
				"updates": map[string]any{
					"checkIntervalHours": float64(8),
				},
			},
			expected: &UpdateConfig{
				Channel:            ChannelStable,
				CheckIntervalHours: 8,
			},
		},
		{
			name: "invalid channel falls back to stable",
			config: map[string]any{
				"updates": map[string]any{
					"channel": "nightly",
				},
			},
			expected: &UpdateConfig{
				Channel: ChannelStable,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.NewConfig(tt.config)
			got := LoadUpdateConfig(cfg)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestUpdateConfigDefaultCheckInterval(t *testing.T) {
	tests := []struct {
		name     string
		config   UpdateConfig
		expected time.Duration
	}{
		{
			name:     "stable default",
			config:   UpdateConfig{Channel: ChannelStable},
			expected: 24 * time.Hour,
		},
		{
			name:     "daily default",
			config:   UpdateConfig{Channel: ChannelDaily},
			expected: 4 * time.Hour,
		},
		{
			name:     "custom override",
			config:   UpdateConfig{Channel: ChannelStable, CheckIntervalHours: 12},
			expected: 12 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.config.DefaultCheckInterval())
		})
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	cfg := config.NewConfig(map[string]any{})

	require.NoError(t, SaveChannel(cfg, ChannelDaily))
	require.NoError(t, SaveAutoUpdate(cfg, true))
	require.NoError(t, SaveCheckIntervalHours(cfg, 6))

	loaded := LoadUpdateConfig(cfg)
	require.Equal(t, ChannelDaily, loaded.Channel)
	require.True(t, loaded.AutoUpdate)
	require.Equal(t, 6, loaded.CheckIntervalHours)
}

func TestIsCacheValid(t *testing.T) {
	future := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	past := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)

	tests := []struct {
		name    string
		cache   *CacheFile
		channel Channel
		want    bool
	}{
		{
			name:    "nil cache",
			cache:   nil,
			channel: ChannelStable,
			want:    false,
		},
		{
			name: "valid stable cache",
			cache: &CacheFile{
				Channel:   "stable",
				Version:   "1.23.6",
				ExpiresOn: future,
			},
			channel: ChannelStable,
			want:    true,
		},
		{
			name: "expired cache",
			cache: &CacheFile{
				Channel:   "stable",
				Version:   "1.23.6",
				ExpiresOn: past,
			},
			channel: ChannelStable,
			want:    false,
		},
		{
			name: "channel mismatch",
			cache: &CacheFile{
				Channel:   "stable",
				Version:   "1.23.6",
				ExpiresOn: future,
			},
			channel: ChannelDaily,
			want:    false,
		},
		{
			name: "missing channel defaults to stable",
			cache: &CacheFile{
				Version:   "1.23.6",
				ExpiresOn: future,
			},
			channel: ChannelStable,
			want:    true,
		},
		{
			name: "missing channel, requesting daily",
			cache: &CacheFile{
				Version:   "1.23.6",
				ExpiresOn: future,
			},
			channel: ChannelDaily,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsCacheValid(tt.cache, tt.channel))
		})
	}
}

func TestCacheFileJSON(t *testing.T) {
	t.Run("new format round-trip", func(t *testing.T) {
		cache := &CacheFile{
			Channel:     "daily",
			Version:     "1.24.0-beta.1",
			BuildNumber: 98770,
			ExpiresOn:   "2026-02-26T08:00:00Z",
		}

		data, err := json.Marshal(cache)
		require.NoError(t, err)

		var loaded CacheFile
		require.NoError(t, json.Unmarshal(data, &loaded))
		require.Equal(t, cache, &loaded)
	})

	t.Run("old format backward compatible", func(t *testing.T) {
		// Old format without channel or buildNumber
		oldJSON := `{"version":"1.23.6","expiresOn":"2026-02-26T01:24:50Z"}`

		var cache CacheFile
		require.NoError(t, json.Unmarshal([]byte(oldJSON), &cache))
		require.Equal(t, "1.23.6", cache.Version)
		require.Equal(t, "", cache.Channel)    // zero value
		require.Equal(t, 0, cache.BuildNumber) // zero value
		require.Equal(t, "2026-02-26T01:24:50Z", cache.ExpiresOn)
	})
}

func TestSaveAndLoadCache(t *testing.T) {
	// Use a temp dir for config
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	cache := &CacheFile{
		Channel:     "daily",
		Version:     "1.24.0-beta.1",
		BuildNumber: 12345,
		ExpiresOn:   time.Now().UTC().Add(4 * time.Hour).Format(time.RFC3339),
	}

	require.NoError(t, SaveCache(cache))

	// Verify file exists
	_, err := os.Stat(filepath.Join(tempDir, cacheFileName))
	require.NoError(t, err)

	loaded, err := LoadCache()
	require.NoError(t, err)
	require.Equal(t, cache.Channel, loaded.Channel)
	require.Equal(t, cache.Version, loaded.Version)
	require.Equal(t, cache.BuildNumber, loaded.BuildNumber)
}

func TestIsPackageManagerInstall(t *testing.T) {
	// This test just ensures the function doesn't panic.
	// The actual result depends on the install method of the test runner.
	_ = IsPackageManagerInstall()
}
