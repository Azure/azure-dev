// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

const (
	// Warning cool down period - don't show same warning within this duration
	warningCoolDownPeriod = 24 * time.Hour
)

// UpdateCheckResult contains the result of checking for extension updates
type UpdateCheckResult struct {
	// HasUpdate is true if a newer version is available
	HasUpdate bool
	// InstalledVersion is the currently installed version
	InstalledVersion string
	// LatestVersion is the latest available version
	LatestVersion string
	// ExtensionId is the ID of the extension
	ExtensionId string
	// ExtensionName is the display name of the extension
	ExtensionName string
}

// UpdateChecker checks for extension updates and manages warning cool downs
type UpdateChecker struct {
	cacheManager *RegistryCacheManager
}

// NewUpdateChecker creates a new update checker
func NewUpdateChecker(cacheManager *RegistryCacheManager) (*UpdateChecker, error) {
	return &UpdateChecker{
		cacheManager: cacheManager,
	}, nil
}

// CheckForUpdate checks if an extension has an available update
func (c *UpdateChecker) CheckForUpdate(
	ctx context.Context,
	extension *Extension,
) (*UpdateCheckResult, error) {
	if extension == nil {
		return nil, errors.New("extension is nil")
	}

	result := &UpdateCheckResult{
		ExtensionId:      extension.Id,
		ExtensionName:    extension.DisplayName,
		InstalledVersion: extension.Version,
		HasUpdate:        false,
	}

	// Get latest version from cache
	latestVersion, err := c.cacheManager.GetExtensionLatestVersion(ctx, extension.Source, extension.Id)
	if err != nil {
		// Cache miss or extension not found - not an error, just no update info
		log.Printf("could not get latest version for %s: %v", extension.Id, err)
		return result, nil
	}

	result.LatestVersion = latestVersion

	// Compare versions using semver
	installed, err := semver.NewVersion(extension.Version)
	if err != nil {
		log.Printf("failed to parse installed version %s: %v", extension.Version, err)
		return result, nil
	}

	latest, err := semver.NewVersion(latestVersion)
	if err != nil {
		log.Printf("failed to parse latest version %s: %v", latestVersion, err)
		return result, nil
	}

	result.HasUpdate = latest.GreaterThan(installed)
	return result, nil
}

// ShouldShowWarning checks if a warning should be shown (respecting cool down)
// Uses the extension's LastUpdateWarning field
func (c *UpdateChecker) ShouldShowWarning(extension *Extension) bool {
	if extension.LastUpdateWarning == "" {
		return true
	}

	lastTime, err := time.Parse(time.RFC3339, extension.LastUpdateWarning)
	if err != nil {
		return true
	}

	return time.Now().UTC().After(lastTime.Add(warningCoolDownPeriod))
}

// RecordWarningShown updates the extension's LastUpdateWarning timestamp
// Returns the updated extension (caller should save it via Manager.UpdateInstalled)
func (c *UpdateChecker) RecordWarningShown(extension *Extension) {
	extension.LastUpdateWarning = time.Now().UTC().Format(time.RFC3339)
}

// FormatUpdateWarning formats the update warning message
func FormatUpdateWarning(result *UpdateCheckResult) *ux.WarningMessage {
	name := result.ExtensionName
	if name == "" {
		name = result.ExtensionId
	}

	return &ux.WarningMessage{
		Description: fmt.Sprintf(
			"A new version of extension '%s' is available: %s -> %s",
			name,
			result.InstalledVersion,
			result.LatestVersion,
		),
		HidePrefix: false,
		Hints: []string{
			fmt.Sprintf("To upgrade: %s",
				output.WithHighLightFormat("azd extension upgrade %s", result.ExtensionId)),
			fmt.Sprintf("To upgrade all: %s",
				output.WithHighLightFormat("azd extension upgrade --all")),
		},
	}
}
