// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/rzip"
	"github.com/denormal/go-gitignore"
)

// CreateDeployableZip creates a zip file of a folder, recursively.
// Returns the path to the created zip file or an error if it fails.
func CreateDeployableZip(appName string, path string) (string, error) {
	// TODO: should probably avoid picking up files that weren't meant to be published (ie, local .env files, etc..)
	zipFile, err := os.CreateTemp("", "azddeploy*.zip")
	if err != nil {
		return "", fmt.Errorf("failed when creating zip package to deploy %s: %w", appName, err)
	}

	if err := rzip.CreateFromDirectory(path, zipFile); err != nil {
		// if we fail here just do our best to close things out and cleanup
		zipFile.Close()
		os.Remove(zipFile.Name())
		return "", err
	}

	if err := zipFile.Close(); err != nil {
		// may fail but, again, we'll do our best to cleanup here.
		os.Remove(zipFile.Name())
		return "", err
	}

	return zipFile.Name(), nil
}

const c_gitIgnore string = ".gitignore"

// CreateSkipPatternsFromGitIgnore inspect root project path and a `servicePath`
// to see if there is a .gitignore file. It then combine both files in a single list
// of exclusions.
func CreateSkipPatternsFromGitIgnore(servicePath string) ([]gitignore.GitIgnore, error) {
	// azdContext will provide the azd-project root path
	azdContext, err := azdcontext.NewAzdContext()
	if err != nil {
		return nil, err
	}

	rootPath := azdContext.ProjectDirectory()
	// If .gitignore can't be open is fine, it could be missing
	var allPatterns []gitignore.GitIgnore
	rootPatterns, _ := gitignore.NewFromFile(filepath.Join(rootPath, c_gitIgnore))
	servicePatterns, _ := gitignore.NewFromFile(filepath.Join(servicePath, c_gitIgnore))

	allPatterns = append(allPatterns, rootPatterns, servicePatterns)
	return allPatterns, nil
}
