// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// FeatureUpdate is the alpha feature key for the azd update command.
var FeatureUpdate = alpha.MustFeatureKey("update")

// Channel represents the update channel for azd builds.
type Channel string

const (
	// ChannelStable represents the stable release channel.
	ChannelStable Channel = "stable"
	// ChannelDaily represents the daily build channel.
	ChannelDaily Channel = "daily"
)

// ParseChannel parses a string into a Channel value.
func ParseChannel(s string) (Channel, error) {
	switch Channel(s) {
	case ChannelStable:
		return ChannelStable, nil
	case ChannelDaily:
		return ChannelDaily, nil
	default:
		return "", fmt.Errorf("invalid channel %q, must be %q or %q", s, ChannelStable, ChannelDaily)
	}
}

const (
	// configKeyChannel is the config key for the update channel.
	configKeyChannel = "updates.channel"
	// configKeyAutoUpdate is the config key for auto-update.
	configKeyAutoUpdate = "updates.autoUpdate"
	// configKeyCheckIntervalHours is the config key for the check interval.
	configKeyCheckIntervalHours = "updates.checkIntervalHours"
)

const (
	// DefaultCheckIntervalStable is the default check interval for the stable channel.
	DefaultCheckIntervalStable = 24 * time.Hour
	// DefaultCheckIntervalDaily is the default check interval for the daily channel.
	DefaultCheckIntervalDaily = 4 * time.Hour
)

// UpdateConfig holds the user's update preferences.
type UpdateConfig struct {
	Channel            Channel
	AutoUpdate         bool
	CheckIntervalHours int
}

// DefaultCheckInterval returns the default check interval for the configured channel.
func (c *UpdateConfig) DefaultCheckInterval() time.Duration {
	if c.CheckIntervalHours > 0 {
		return time.Duration(c.CheckIntervalHours) * time.Hour
	}

	if c.Channel == ChannelDaily {
		return DefaultCheckIntervalDaily
	}

	return DefaultCheckIntervalStable
}

// LoadUpdateConfig reads update configuration from the user config.
func LoadUpdateConfig(cfg config.Config) *UpdateConfig {
	uc := &UpdateConfig{
		Channel: ChannelStable,
	}

	if ch, ok := cfg.GetString(configKeyChannel); ok {
		if parsed, err := ParseChannel(ch); err == nil {
			uc.Channel = parsed
		}
	}

	if au, ok := cfg.GetString(configKeyAutoUpdate); ok {
		uc.AutoUpdate = au == "on"
	}

	if interval, ok := cfg.Get(configKeyCheckIntervalHours); ok {
		switch v := interval.(type) {
		case float64:
			uc.CheckIntervalHours = int(v)
		case int:
			uc.CheckIntervalHours = v
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				uc.CheckIntervalHours = n
			}
		}
	}

	return uc
}

// SaveChannel persists the channel to user config.
func SaveChannel(cfg config.Config, channel Channel) error {
	return cfg.Set(configKeyChannel, string(channel))
}

// SaveAutoUpdate persists the auto-update setting to user config.
func SaveAutoUpdate(cfg config.Config, enabled bool) error {
	value := "off"
	if enabled {
		value = "on"
	}
	return cfg.Set(configKeyAutoUpdate, value)
}

// SaveCheckIntervalHours persists the check interval to user config.
func SaveCheckIntervalHours(cfg config.Config, hours int) error {
	return cfg.Set(configKeyCheckIntervalHours, hours)
}

// CacheFile represents the cached version check result.
type CacheFile struct {
	// Channel is the update channel this cache entry is for.
	Channel string `json:"channel,omitempty"`
	// Version is the semver of the latest version.
	Version string `json:"version"`
	// BuildNumber is the Azure DevOps build ID (used for daily builds).
	BuildNumber int `json:"buildNumber,omitempty"`
	// ExpiresOn is the time at which this cached value expires, stored as an RFC3339 timestamp.
	ExpiresOn string `json:"expiresOn"`
}

const cacheFileName = "update-check.json"

// LoadCache reads the cached version check result.
func LoadCache() (*CacheFile, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("could not determine config directory: %w", err)
	}

	cacheFilePath := filepath.Join(configDir, cacheFileName)
	data, err := os.ReadFile(cacheFilePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("error reading update cache file: %w", err)
	}

	var cache CacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("could not unmarshal cache file: %w", err)
	}

	return &cache, nil
}

// SaveCache writes the cached version check result.
func SaveCache(cache *CacheFile) error {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return fmt.Errorf("could not determine config directory: %w", err)
	}

	cacheFilePath := filepath.Join(configDir, cacheFileName)
	if err := os.MkdirAll(filepath.Dir(cacheFilePath), osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("failed to create cache folder: %w", err)
	}

	data, _ := json.Marshal(cache)
	if err := os.WriteFile(cacheFilePath, data, osutil.PermissionFile); err != nil {
		return fmt.Errorf("failed to write update cache file: %w", err)
	}

	log.Printf("updated cache file to version %s (expires on: %s)", cache.Version, cache.ExpiresOn)
	return nil
}

// IsCacheValid checks if the cache is still valid (not expired) and matches the given channel.
func IsCacheValid(cache *CacheFile, channel Channel) bool {
	if cache == nil {
		return false
	}

	// If cache has no channel, treat as stable (backward compatibility)
	cacheChannel := Channel(cache.Channel)
	if cacheChannel == "" {
		cacheChannel = ChannelStable
	}

	if cacheChannel != channel {
		return false
	}

	expiresOn, err := time.Parse(time.RFC3339, cache.ExpiresOn)
	if err != nil {
		return false
	}

	return time.Now().UTC().Before(expiresOn)
}
