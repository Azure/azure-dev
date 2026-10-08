// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdcontext

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// EnvironmentNameRegexp matches the characters and length allowed in deployment names.
var EnvironmentNameRegexp = regexp.MustCompile(`^[a-zA-Z0-9-\(\)_\.]{1,64}$`)

// IsValidEnvironmentName reports whether name is a deployment name and a local directory name.
func IsValidEnvironmentName(name string) bool {
	if runtime.GOOS == "windows" && strings.HasSuffix(name, ".") {
		// Windows strips trailing periods when resolving directory names, so distinct names
		// could otherwise refer to the same environment directory.
		return false
	}
	// Dot-only names can resolve to the current or parent directory, including
	// through Windows trailing-dot normalization.
	return EnvironmentNameRegexp.MatchString(name) && strings.Trim(name, ".") != "" && filepath.IsLocal(name)
}

// InvalidEnvironmentNameError returns a standardized error for an invalid environment name.
func InvalidEnvironmentNameError(name string) error {
	return fmt.Errorf(
		"environment name '%s' is invalid (use 1-64 alphanumeric characters, hyphens, underscores, "+
			"parentheses or periods; the name must be a valid directory name, not a path, "+
			"and cannot contain only periods; trailing periods are not allowed on Windows)",
		name,
	)
}
