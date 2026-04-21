// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
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

	v, err := semver.NewVersion(schemaVersion)
	if err != nil {
		return fmt.Errorf(
			"invalid registry schema version %q: %w",
			schemaVersion, err,
		)
	}

	if v.Major() > uint64(MinSupportedMajorVersion) {
		return &ErrUnsupportedRegistrySchema{
			SchemaVersion:       schemaVersion,
			MaxSupportedVersion: CurrentRegistrySchemaVersion,
		}
	}

	return nil
}
