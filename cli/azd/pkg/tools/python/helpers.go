// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package python

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// VenvNameForDir computes a virtual environment directory name
// from the given project directory path, using the naming
// convention {baseName}_env. Both the framework service and the
// hook executor share this convention.
func VenvNameForDir(projectDir string) string {
	trimmed := strings.TrimSpace(projectDir)
	if len(trimmed) > 0 &&
		trimmed[len(trimmed)-1] == os.PathSeparator {
		trimmed = trimmed[:len(trimmed)-1]
	}
	_, base := filepath.Split(trimmed)
	return base + "_env"
}

// VenvPythonPath returns the path to the Python executable
// inside the given virtual environment directory. On Windows
// this is Scripts/python.exe; on other platforms bin/python.
func VenvPythonPath(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(
			venvDir, "Scripts", "python.exe",
		)
	}
	return filepath.Join(venvDir, "bin", "python")
}

// VenvActivateCmd returns the shell command or path used to
// activate the given virtual environment. On Windows it
// returns the Scripts/activate path; on other platforms it
// returns ". bin/activate" suitable for sourcing.
func VenvActivateCmd(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(
			venvDir, "Scripts", "activate",
		)
	}
	return ". " + filepath.Join(venvDir, "bin", "activate")
}
