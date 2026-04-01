// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrProjectNotFound is returned when azure.yaml cannot be located.
var ErrProjectNotFound = errors.New("azure.yaml not found")

// projectFileName is the expected project file at the root of an azd project.
const projectFileName = "azure.yaml"

// GetProjectDir returns the azd project directory.
// It checks AZD_EXEC_PROJECT_DIR env var first, then walks up from cwd
// looking for azure.yaml.
func GetProjectDir() (string, error) {
	// Strategy 1: explicit environment variable override.
	if dir := os.Getenv("AZD_EXEC_PROJECT_DIR"); dir != "" {
		return dir, nil
	}

	// Strategy 2: walk up from the current working directory.
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("azdext.GetProjectDir: failed to get working directory: %w", err)
	}

	dir, err := FindFileUpward(cwd, projectFileName)
	if err != nil {
		return "", err
	}

	return dir, nil
}

// FindFileUpward searches for a file by name starting from startDir,
// walking up parent directories until found or root is reached.
// Returns the directory containing the file, not the full file path.
func FindFileUpward(startDir string, fileName string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("azdext.FindFileUpward: failed to resolve absolute path: %w", err)
	}

	for {
		candidate := filepath.Join(dir, fileName)

		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding the file.
			return "", ErrProjectNotFound
		}

		dir = parent
	}
}
