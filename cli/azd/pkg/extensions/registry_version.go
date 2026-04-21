// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"fmt"
	"strconv"
	"strings"
)

// ErrUnsupportedRegistrySchema is returned when the registry schema version
// is newer than what this version of azd supports.
type ErrUnsupportedRegistrySchema struct {
	// SchemaVersion is the version found in the registry
	SchemaVersion string
	// MaxSupportedVersion is the maximum version azd supports
	MaxSupportedVersion string
}

// Error returns a descriptive error message for the unsupported schema.
func (e *ErrUnsupportedRegistrySchema) Error() string {
	return fmt.Sprintf(
		"registry schema version %s is not supported (max supported: %s)",
		e.SchemaVersion, e.MaxSupportedVersion,
	)
}

// CheckRegistrySchemaVersion validates that the given schema version
// is compatible with this version of azd.
//
// Rules:
//   - Empty or missing version is treated as "1.0" (backward compatible)
//   - Same major version with a newer minor is accepted silently
//   - A higher major version returns ErrUnsupportedRegistrySchema
//   - Malformed versions return a descriptive parse error
func CheckRegistrySchemaVersion(schemaVersion string) error {
	if schemaVersion == "" {
		return nil
	}

	major, minor, err := parseSchemaVersion(schemaVersion)
	if err != nil {
		return err
	}

	_ = minor // newer minor versions are accepted silently

	if major > MinSupportedMajorVersion {
		return &ErrUnsupportedRegistrySchema{
			SchemaVersion:       schemaVersion,
			MaxSupportedVersion: CurrentRegistrySchemaVersion,
		}
	}

	return nil
}

// parseSchemaVersion parses a "major.minor" version string into its
// integer components. Returns an error for malformed input.
func parseSchemaVersion(version string) (int, int, error) {
	majorStr, minorStr, ok := strings.Cut(version, ".")
	if !ok {
		return 0, 0, fmt.Errorf(
			"invalid registry schema version %q: expected major.minor format",
			version,
		)
	}

	// Reject extra dot segments (e.g. "1.0.0")
	if strings.Contains(minorStr, ".") {
		return 0, 0, fmt.Errorf(
			"invalid registry schema version %q: expected major.minor format",
			version,
		)
	}

	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return 0, 0, fmt.Errorf(
			"invalid registry schema version %q: cannot parse major component",
			version,
		)
	}

	if major < 0 {
		return 0, 0, fmt.Errorf(
			"invalid registry schema version %q: major version cannot be negative",
			version,
		)
	}

	minor, err := strconv.Atoi(minorStr)
	if err != nil {
		return 0, 0, fmt.Errorf(
			"invalid registry schema version %q: cannot parse minor component",
			version,
		)
	}

	if minor < 0 {
		return 0, 0, fmt.Errorf(
			"invalid registry schema version %q: minor version cannot be negative",
			version,
		)
	}

	return major, minor, nil
}
