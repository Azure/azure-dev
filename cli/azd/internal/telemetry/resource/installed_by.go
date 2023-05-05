// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resource

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
)

const cInstalledByFileName = ".installed-by.txt"

// Returns a hash of the content of `.installed-by.txt` file in the same directory as
// the executable. If the file does not exist, returns empty string.
func getInstalledBy() string {
	exePath, err := os.Executable()

	if err != nil {
		return ""
	}

	resolvedPath, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return ""
	}

	exeDir := filepath.Dir(resolvedPath)
	installedByFile := filepath.Join(exeDir, cInstalledByFileName)

	bytes, err := os.ReadFile(installedByFile)
	if err != nil {
		return ""
	}

	return fields.Sha256Hash(strings.TrimSpace(string(bytes)))
}
