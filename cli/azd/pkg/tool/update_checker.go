// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const (
	// configKeyLastUpdateCheck stores the RFC3339 timestamp of the last tool
	// update check.
	configKeyLastUpdateCheck = "tool.lastUpdateCheck"
	// configKeyCheckIntervalHours stores how many hours to wait between
	// automatic update checks.
	configKeyCheckIntervalHours = "tool.checkIntervalHours"
	// configKeyUpdateChecks enables or disables automatic update checks.
	// Valid values are "on" (default) and "off".
	configKeyUpdateChecks = "tool.updateChecks"
	// configKeyLastNotificationShown stores the RFC3339 timestamp of the
	// last time an update notification was displayed.
	configKeyLastNotificationShown = "tool.lastNotificationShown"

	// defaultCheckIntervalHours is how often (in hours) update checks run
	// when the user has not overridden the interval. 168 h = 7 days.
	defaultCheckIntervalHours = 168

	// toolCheckCacheFileName is the file name used for the tool update
	// check cache inside the azd config directory.
	toolCheckCacheFileName = "tool-check-cache.json"
)

// CachedToolVersion stores the latest known version of a single tool.
type CachedToolVersion struct {
	// LatestVersion is the most recent version string returned by the
	// remote version API (or left empty when no remote data is available).
	LatestVersion string `json:"latestVersion"`
}

// UpdateCheckCache is the on-disk representation of a tool update check
// result set. It is serialized as JSON and written to
// ~/.azd/tool-check-cache.json.
type UpdateCheckCache struct {
	// CheckedAt is the time the cache was last populated.
	CheckedAt time.Time `json:"checkedAt"`
	// ExpiresAt is the earliest time the cache should be refreshed.
	ExpiresAt time.Time `json:"expiresAt"`
	// Tools maps tool IDs to their cached version information.
	Tools map[string]CachedToolVersion `json:"tools"`
}

// UpdateCheckResult pairs a tool definition with its current and latest
// version information so callers can determine whether an upgrade is
// available.
type UpdateCheckResult struct {
	// Tool is the registry definition that was checked.
	Tool *ToolDefinition
	// CurrentVersion is the version currently installed on the local
	// machine (empty when the tool is not installed).
	CurrentVersion string
	// LatestVersion is the newest version known to the update checker.
	LatestVersion string
	// UpdateAvailable is true when LatestVersion is non-empty and
	// differs from CurrentVersion.
	UpdateAvailable bool
}

// UpdateChecker performs periodic update checks for registered tools,
// caching results to disk so that expensive remote lookups are amortized
// across CLI invocations.
type UpdateChecker struct {
	configManager config.UserConfigManager
	detector      Detector
	cacheFilePath string
}

// NewUpdateChecker creates an [UpdateChecker] that stores its cache
// file inside azdConfigDir.
func NewUpdateChecker(
	configManager config.UserConfigManager,
	detector Detector,
	azdConfigDir string,
) *UpdateChecker {
	return &UpdateChecker{
		configManager: configManager,
		detector:      detector,
		cacheFilePath: filepath.Join(azdConfigDir, toolCheckCacheFileName),
	}
}

// ShouldCheck returns true when enough time has elapsed since the last
// update check and automatic checks have not been disabled by the user.
func (uc *UpdateChecker) ShouldCheck(ctx context.Context) bool {
	cfg, err := uc.configManager.Load()
	if err != nil {
		log.Printf("update-checker: failed to load config: %v", err)
		return false
	}

	// Respect the kill-switch.
	if mode, ok := cfg.GetString(configKeyUpdateChecks); ok {
		if mode == "off" {
			return false
		}
	}

	intervalHours := loadIntervalHours(cfg)

	lastCheckStr, ok := cfg.GetString(configKeyLastUpdateCheck)
	if !ok {
		// Never checked before — check now.
		return true
	}

	lastCheck, err := time.Parse(time.RFC3339, lastCheckStr)
	if err != nil {
		log.Printf(
			"update-checker: invalid %s value %q: %v",
			configKeyLastUpdateCheck, lastCheckStr, err,
		)
		return true
	}

	return time.Now().After(
		lastCheck.Add(time.Duration(intervalHours) * time.Hour),
	)
}

// Check runs the update check for the supplied tools. It detects the
// currently installed versions, compares them against cached remote
// data, persists the results, and updates the last-check timestamp.
//
// In this POC implementation there is no remote version API; the
// latest-version field comes solely from any previously cached data.
func (uc *UpdateChecker) Check(
	ctx context.Context,
	tools []*ToolDefinition,
) ([]*UpdateCheckResult, error) {
	statuses, err := uc.detector.DetectAll(ctx, tools)
	if err != nil {
		return nil, fmt.Errorf("detecting installed tools: %w", err)
	}

	existing, _ := uc.GetCachedResults()

	statusByID := make(map[string]*ToolStatus, len(statuses))
	for _, s := range statuses {
		statusByID[s.Tool.Id] = s
	}

	intervalHours := uc.loadConfiguredInterval()
	now := time.Now().UTC()
	cache := &UpdateCheckCache{
		CheckedAt: now,
		ExpiresAt: now.Add(time.Duration(intervalHours) * time.Hour),
		Tools:     make(map[string]CachedToolVersion, len(tools)),
	}

	results := make([]*UpdateCheckResult, 0, len(tools))
	for _, t := range tools {
		status := statusByID[t.Id]

		var currentVer string
		if status != nil {
			currentVer = status.InstalledVersion
		}

		// POC: carry forward any previously cached latest version.
		var latestVer string
		if existing != nil {
			if cached, ok := existing.Tools[t.Id]; ok {
				latestVer = cached.LatestVersion
			}
		}

		cache.Tools[t.Id] = CachedToolVersion{
			LatestVersion: latestVer,
		}

		results = append(results, &UpdateCheckResult{
			Tool:            t,
			CurrentVersion:  currentVer,
			LatestVersion:   latestVer,
			UpdateAvailable: latestVer != "" && latestVer != currentVer,
		})
	}

	if err := uc.SaveCache(cache); err != nil {
		return results, fmt.Errorf("saving update cache: %w", err)
	}

	if err := uc.recordCheckTimestamp(); err != nil {
		return results, fmt.Errorf("recording check timestamp: %w", err)
	}

	return results, nil
}

// GetCachedResults reads and returns the on-disk update check cache.
// It returns (nil, nil) when the cache file does not yet exist.
func (uc *UpdateChecker) GetCachedResults() (*UpdateCheckCache, error) {
	data, err := os.ReadFile(uc.cacheFilePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("reading tool check cache: %w", err)
	}

	var cache UpdateCheckCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("unmarshalling tool check cache: %w", err)
	}

	return &cache, nil
}

// SaveCache serializes the cache to disk, creating any intermediate
// directories as needed.
func (uc *UpdateChecker) SaveCache(cache *UpdateCheckCache) error {
	dir := filepath.Dir(uc.cacheFilePath)
	if err := os.MkdirAll(dir, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling tool check cache: %w", err)
	}

	if err := os.WriteFile(
		uc.cacheFilePath, data, osutil.PermissionFile,
	); err != nil {
		return fmt.Errorf("writing tool check cache: %w", err)
	}

	return nil
}

// HasUpdatesAvailable checks the cache for tools whose latest known
// version differs from the currently installed version.
// It returns whether any updates exist, how many, and any error
// encountered while reading the cache or detecting versions.
func (uc *UpdateChecker) HasUpdatesAvailable(
	ctx context.Context,
) (bool, int, error) {
	cache, err := uc.GetCachedResults()
	if err != nil {
		return false, 0, fmt.Errorf("loading cached results: %w", err)
	}

	if cache == nil || len(cache.Tools) == 0 {
		return false, 0, nil
	}

	// Build a lookup of tool IDs to their cached latest versions, but
	// only for entries that actually have a known latest version.
	candidates := make(map[string]string, len(cache.Tools))
	for id, ct := range cache.Tools {
		if ct.LatestVersion != "" {
			candidates[id] = ct.LatestVersion
		}
	}

	if len(candidates) == 0 {
		return false, 0, nil
	}

	// Resolve tool definitions so we can detect installed versions.
	var toolDefs []*ToolDefinition
	for id := range candidates {
		if t := FindTool(id); t != nil {
			toolDefs = append(toolDefs, t)
		}
	}

	if len(toolDefs) == 0 {
		return false, 0, nil
	}

	statuses, err := uc.detector.DetectAll(ctx, toolDefs)
	if err != nil {
		return false, 0, fmt.Errorf("detecting tools: %w", err)
	}

	count := 0
	for _, s := range statuses {
		latest, ok := candidates[s.Tool.Id]
		if ok && latest != s.InstalledVersion {
			count++
		}
	}

	return count > 0, count, nil
}

// MarkNotificationShown records the current time as the last moment an
// update notification was displayed, preventing repeated notifications
// within the same check cycle.
func (uc *UpdateChecker) MarkNotificationShown(
	ctx context.Context,
) error {
	cfg, err := uc.configManager.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if err := cfg.Set(
		configKeyLastNotificationShown,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("setting %s: %w", configKeyLastNotificationShown, err)
	}

	if err := uc.configManager.Save(cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	return nil
}

// ShouldShowNotification returns true when an update notification has
// not yet been shown for the most recent check cycle.
func (uc *UpdateChecker) ShouldShowNotification(
	ctx context.Context,
) bool {
	cfg, err := uc.configManager.Load()
	if err != nil {
		log.Printf("update-checker: failed to load config: %v", err)
		return false
	}

	lastCheckStr, hasCheck := cfg.GetString(configKeyLastUpdateCheck)
	if !hasCheck {
		// No check has been performed yet — nothing to notify about.
		return false
	}

	lastCheck, err := time.Parse(time.RFC3339, lastCheckStr)
	if err != nil {
		return false
	}

	shownStr, hasShown := cfg.GetString(configKeyLastNotificationShown)
	if !hasShown {
		// A check exists but no notification has ever been shown.
		return true
	}

	lastShown, err := time.Parse(time.RFC3339, shownStr)
	if err != nil {
		return true
	}

	// Only show once per check cycle: skip if the notification was
	// already displayed after the most recent check.
	return lastShown.Before(lastCheck)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// loadIntervalHours reads the check-interval configuration from a
// pre-loaded [config.Config], falling back to the default when the key
// is unset or unparsable.
func loadIntervalHours(cfg config.Config) int {
	raw, ok := cfg.Get(configKeyCheckIntervalHours)
	if !ok {
		return defaultCheckIntervalHours
	}

	switch v := raw.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}

	return defaultCheckIntervalHours
}

// loadConfiguredInterval loads the interval from the user config,
// returning the default when the config is unavailable.
func (uc *UpdateChecker) loadConfiguredInterval() int {
	cfg, err := uc.configManager.Load()
	if err != nil {
		return defaultCheckIntervalHours
	}

	return loadIntervalHours(cfg)
}

// recordCheckTimestamp persists the current UTC time as the
// last-update-check timestamp in the user config.
func (uc *UpdateChecker) recordCheckTimestamp() error {
	cfg, err := uc.configManager.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if err := cfg.Set(
		configKeyLastUpdateCheck,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("setting %s: %w", configKeyLastUpdateCheck, err)
	}

	if err := uc.configManager.Save(cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	return nil
}
