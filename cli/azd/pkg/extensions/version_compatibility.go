// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"log"

	"github.com/Masterminds/semver/v3"
)

// VersionIsCompatible checks if an extension version is compatible with the given azd version.
// Returns true if:
// - No MinAzdVersion is set on the extension version
// - The azdVersion meets the minimum requirement
//
// Note: Non-production builds (dev, PR, daily) should be handled by the caller
// by not calling this function or passing nil to FilterCompatibleVersions.
func VersionIsCompatible(extVersion *ExtensionVersion, azdVersion *semver.Version) bool {
	if extVersion.MinAzdVersion == "" {
		return true
	}

	minVersion, err := semver.NewVersion(extVersion.MinAzdVersion)
	if err != nil {
		log.Printf("Warning: Failed to parse minAzdVersion '%s', skipping compatibility check", extVersion.MinAzdVersion)
		return true
	}

	return !azdVersion.LessThan(minVersion)
}

// VersionCompatibilityResult holds the result of filtering extension versions for compatibility
type VersionCompatibilityResult struct {
	// Compatible contains only the extension versions compatible with the current azd version
	Compatible []ExtensionVersion
	// LatestOverall is the latest version available regardless of compatibility
	LatestOverall *ExtensionVersion
	// LatestCompatible is the latest version that is compatible with the current azd
	LatestCompatible *ExtensionVersion
	// HasNewerIncompatible is true when a newer version exists but is not compatible
	HasNewerIncompatible bool
}

// FilterCompatibleVersions filters extension versions based on compatibility with the current azd version.
// It returns a result containing compatible versions and information about incompatible newer versions.
func FilterCompatibleVersions(
	versions []ExtensionVersion,
	azdVersion *semver.Version,
) *VersionCompatibilityResult {
	result := &VersionCompatibilityResult{}

	if len(versions) == 0 {
		return result
	}

	// The latest overall is always the last element (versions are ordered)
	result.LatestOverall = &versions[len(versions)-1]

	for i := range versions {
		if VersionIsCompatible(&versions[i], azdVersion) {
			result.Compatible = append(result.Compatible, versions[i])
			result.LatestCompatible = &versions[i]
		}
	}

	// Check if there's a newer incompatible version
	if result.LatestCompatible != nil && result.LatestOverall != nil {
		latestCompatibleSemver, err1 := semver.NewVersion(result.LatestCompatible.Version)
		latestOverallSemver, err2 := semver.NewVersion(result.LatestOverall.Version)
		if err1 == nil && err2 == nil {
			result.HasNewerIncompatible = latestOverallSemver.GreaterThan(latestCompatibleSemver)
		}
	} else if result.LatestCompatible == nil && result.LatestOverall != nil {
		result.HasNewerIncompatible = true
	}

	return result
}
