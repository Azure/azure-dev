// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import "strings"

// LocalErrorCategory is the canonical category type for extension local errors.
// Keep values aligned with telemetry ResultCode families in internal/cmd/errors.go.
type LocalErrorCategory string

const (
	LocalErrorCategoryValidation    LocalErrorCategory = "validation"
	LocalErrorCategoryAuth          LocalErrorCategory = "auth"
	LocalErrorCategoryDependency    LocalErrorCategory = "dependency"
	LocalErrorCategoryCompatibility LocalErrorCategory = "compatibility"
	LocalErrorCategoryUser          LocalErrorCategory = "user"
	LocalErrorCategoryInternal      LocalErrorCategory = "internal"
	LocalErrorCategoryLocal         LocalErrorCategory = "local"
)

// NormalizeLocalErrorCategory validates a typed category value, returning the canonical constant.
// Unknown values are collapsed to LocalErrorCategoryLocal.
func NormalizeLocalErrorCategory(category LocalErrorCategory) LocalErrorCategory {
	switch LocalErrorCategory(strings.ToLower(strings.TrimSpace(string(category)))) {
	case LocalErrorCategoryValidation:
		return LocalErrorCategoryValidation
	case LocalErrorCategoryAuth:
		return LocalErrorCategoryAuth
	case LocalErrorCategoryDependency:
		return LocalErrorCategoryDependency
	case LocalErrorCategoryCompatibility:
		return LocalErrorCategoryCompatibility
	case LocalErrorCategoryUser:
		return LocalErrorCategoryUser
	case LocalErrorCategoryInternal:
		return LocalErrorCategoryInternal
	default:
		return LocalErrorCategoryLocal
	}
}

// ParseLocalErrorCategory parses a raw category string (e.g. from proto deserialization)
// into its canonical LocalErrorCategory constant. Unknown values map to LocalErrorCategoryLocal.
func ParseLocalErrorCategory(category string) LocalErrorCategory {
	return NormalizeLocalErrorCategory(LocalErrorCategory(category))
}
