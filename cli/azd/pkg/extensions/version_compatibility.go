// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"log"

	"github.com/Masterminds/semver/v3"
)

// VersionIsCompatible checks if an extension version is compatible with the given azd version.
// Returns true if:
// - No RequiredAzdVersion is set on the extension version
// - The azdVersion satisfies the RequiredAzdVersion constraint expression
//
// RequiredAzdVersion supports semantic versioning constraint expressions (e.g. ">= 1.24.0").
func VersionIsCompatible(extVersion *ExtensionVersion, azdVersion *semver.Version) bool {
	if extVersion.RequiredAzdVersion == "" {
		return true
	}

	constraint, err := semver.NewConstraint(extVersion.RequiredAzdVersion)
	if err != nil {
		log.Printf(
			"Warning: Failed to parse requiredAzdVersion constraint '%s', skipping compatibility check",
			extVersion.RequiredAzdVersion,
		)
		return true
	}

	return constraint.Check(azdVersion)
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

// LatestVersion returns the ExtensionVersion with the highest semantic version from the provided slice.
// It uses semver comparison so the result is correct regardless of the order versions are listed in.
// Returns nil if the slice is empty; always returns a non-nil pointer for non-empty slices
// (falls back to the first element if all version strings fail to parse).
func LatestVersion(versions []ExtensionVersion) *ExtensionVersion {
	if len(versions) == 0 {
		return nil
	}

	latest := &versions[0]
	latestSemver, err := semver.NewVersion(latest.Version)
	if err != nil {
		// First version string didn't parse; latestSemver stays nil so any
		// parseable candidate will replace it in the loop below.
		latestSemver = nil
	}

	for i := 1; i < len(versions); i++ {
		candidate, err := semver.NewVersion(versions[i].Version)
		if err != nil {
			continue
		}
		if latestSemver == nil || candidate.GreaterThan(latestSemver) {
			latest = &versions[i]
			latestSemver = candidate
		}
	}

	return latest
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

	// Find the latest overall version using semver comparison (order-independent).
	// Store a copy so the result doesn't alias the caller's slice.
	latestOverall := *LatestVersion(versions)
	result.LatestOverall = &latestOverall

	for i := range versions {
		if VersionIsCompatible(&versions[i], azdVersion) {
			result.Compatible = append(result.Compatible, versions[i])
		}
	}

	if len(result.Compatible) > 0 {
		result.LatestCompatible = LatestVersion(result.Compatible)
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
