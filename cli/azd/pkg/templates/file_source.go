// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templates

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// newFileTemplateSource creates a new template source from a file.
func newFileTemplateSource(name string, path string) (Source, error) {
	absolutePath, err := getAbsolutePath(path)
	if err != nil {
		return nil, fmt.Errorf("failed converting path '%s' to absolute path, %w", path, err)
	}

	templateBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading file '%s', %w", path, err)
	}

	return newJsonTemplateSource(name, string(templateBytes))
}

func getAbsolutePath(filePath string) (string, error) {
	// Check if the path is absolute
	if filepath.IsAbs(filePath) {
		return filePath, nil
	}

	roots := []string{}

	// Get the current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	azdConfigPath, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}

	roots = append(roots, cwd)
	roots = append(roots, azdConfigPath)

	return osutil.ResolveContainedPath(roots, filePath)
}
