// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"fmt"
	"log"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/internal"
)

// CurrentAzdVersion returns the version used for extension compatibility checks.
// It returns nil for development builds and removes prerelease tags.
func CurrentAzdVersion() *semver.Version {
	if internal.IsDevVersion() {
		return nil
	}

	versionInfo := internal.VersionInfo()
	version, err := semver.NewVersion(versionInfo.Version.String())
	if err != nil {
		return nil
	}
	if version.Prerelease() == "" {
		return version
	}

	stripped, err := semver.NewVersion(fmt.Sprintf(
		"%d.%d.%d",
		version.Major(),
		version.Minor(),
		version.Patch(),
	))
	if err != nil {
		return nil
	}
	return stripped
}

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

// CompareExtensionVersions compares two versions using semantic versioning and any declared
// version migration. A migrated From version sorts immediately before its To version, so To
// and all later semantic versions are considered successors.
func CompareExtensionVersions(extension *ExtensionMetadata, left, right string) int {
	if left == right {
		return 0
	}

	leftVersion, leftErr := semver.NewVersion(left)
	rightVersion, rightErr := semver.NewVersion(right)
	if leftErr != nil || rightErr != nil {
		return 0
	}

	leftFloor := versionMigrationFloor(extension, left)
	rightFloor := versionMigrationFloor(extension, right)
	switch {
	case leftFloor != nil && rightFloor != nil:
		if comparison := leftFloor.Compare(rightFloor); comparison != 0 {
			return comparison
		}
		return leftVersion.Compare(rightVersion)
	case leftFloor != nil:
		if rightVersion.LessThan(leftFloor) {
			return 1
		}
		return -1
	case rightFloor != nil:
		if leftVersion.LessThan(rightFloor) {
			return -1
		}
		return 1
	default:
		return leftVersion.Compare(rightVersion)
	}
}

func versionMigrationFloor(extension *ExtensionMetadata, version string) *semver.Version {
	if extension == nil {
		return nil
	}
	for _, migration := range extension.VersionMigrations {
		if migration.From != version {
			continue
		}
		from, err := semver.StrictNewVersion(migration.From)
		if err != nil {
			return nil
		}
		floor, err := semver.StrictNewVersion(migration.To)
		if err == nil && from.GreaterThan(floor) {
			return floor
		}
		return nil
	}
	return nil
}

// IsExtensionVersionUpgrade reports whether target is newer than current after applying
// the extension's explicitly declared migration ordering.
func IsExtensionVersionUpgrade(extension *ExtensionMetadata, current, target string) bool {
	return CompareExtensionVersions(extension, target, current) > 0
}

// IsExtensionVersionDowngrade reports whether target is older than current after applying
// the extension's explicitly declared migration ordering.
func IsExtensionVersionDowngrade(extension *ExtensionMetadata, current, target string) bool {
	return CompareExtensionVersions(extension, target, current) < 0
}

// LatestVersion returns the ExtensionVersion with the highest semantic version from the provided slice.
// It compares all elements using strict semver ordering so the result is correct regardless of slice ordering.
// Returns nil if the slice is empty.
func LatestVersion(versions []ExtensionVersion) *ExtensionVersion {
	return latestExtensionVersion(nil, versions)
}

// LatestExtensionVersion returns the highest version after applying the extension's declared
// migration ordering.
func LatestExtensionVersion(extension *ExtensionMetadata) *ExtensionVersion {
	if extension == nil {
		return nil
	}
	return latestExtensionVersion(extension, extension.Versions)
}

func latestExtensionVersion(
	extension *ExtensionMetadata,
	versions []ExtensionVersion,
) *ExtensionVersion {
	if len(versions) == 0 {
		return nil
	}

	var latest *ExtensionVersion

	for i := range versions {
		_, err := semver.NewVersion(versions[i].Version)
		if err != nil {
			log.Printf("Warning: failed to parse extension version '%s': %v", versions[i].Version, err)
			continue
		}
		if latest == nil ||
			CompareExtensionVersions(extension, versions[i].Version, latest.Version) > 0 {
			latest = &versions[i]
		}
	}

	if latest == nil {
		// All version strings failed to parse; fall back to the last element.
		return &versions[len(versions)-1]
	}

	return latest
}

// FilterCompatibleVersions filters extension versions based on compatibility with the current azd version.
// It returns a result containing compatible versions and information about incompatible newer versions.
func FilterCompatibleVersions(
	versions []ExtensionVersion,
	azdVersion *semver.Version,
) *VersionCompatibilityResult {
	return filterCompatibleVersions(nil, versions, azdVersion)
}

// FilterCompatibleExtensionVersions applies azd compatibility and declared migration ordering
// to an extension's published versions.
func FilterCompatibleExtensionVersions(
	extension *ExtensionMetadata,
	azdVersion *semver.Version,
) *VersionCompatibilityResult {
	if extension == nil {
		return &VersionCompatibilityResult{}
	}
	return filterCompatibleVersions(extension, extension.Versions, azdVersion)
}

func filterCompatibleVersions(
	extension *ExtensionMetadata,
	versions []ExtensionVersion,
	azdVersion *semver.Version,
) *VersionCompatibilityResult {
	result := &VersionCompatibilityResult{}

	if len(versions) == 0 {
		return result
	}

	// Find the latest overall version using semver comparison (order-independent).
	// Store a copy so the result doesn't alias the caller's slice.
	latestOverall := *latestExtensionVersion(extension, versions) // safe: len(versions) > 0 checked above
	result.LatestOverall = &latestOverall

	for i := range versions {
		if VersionIsCompatible(&versions[i], azdVersion) {
			result.Compatible = append(result.Compatible, versions[i])
		}
	}

	if len(result.Compatible) > 0 {
		result.LatestCompatible = latestExtensionVersion(extension, result.Compatible)
	}

	// Check if there's a newer incompatible version
	if result.LatestCompatible != nil && result.LatestOverall != nil {
		result.HasNewerIncompatible = CompareExtensionVersions(
			extension,
			result.LatestOverall.Version,
			result.LatestCompatible.Version,
		) > 0
	} else if result.LatestCompatible == nil && result.LatestOverall != nil {
		result.HasNewerIncompatible = true
	}

	return result
}
