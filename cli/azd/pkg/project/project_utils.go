// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/dotignore"
	"github.com/azure/azure-dev/cli/azd/pkg/rzip"
	"github.com/otiai10/copy"
)

// CreateDeployableZip creates a zip file of a folder, recursively.
// Returns the path to the created zip file or an error if it fails.
func createDeployableZip(projectName string, appName string, path string) (string, error) {
	// TODO: should probably avoid picking up files that weren't meant to be deployed (ie, local .env files, etc..)
	filePath := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%s-azddeploy-%d.zip", projectName, appName, time.Now().Unix()))
	zipFile, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("failed when creating zip package to deploy %s: %w", appName, err)
	}

	// Read and honor the .dotignore files
	ignoreMatchers, err := dotignore.ReadIgnoreFiles(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("reading .dotignore files: %w", err)
	}

	// Create the zip file, excluding files that match the .dotignore rules
	err = rzip.CreateFromDirectoryWithIgnore(path, zipFile, ignoreMatchers)
	if err != nil {
		// If we fail here, just do our best to close things out and cleanup
		zipFile.Close()
		os.Remove(zipFile.Name())
		return "", err
	}

	if err := zipFile.Close(); err != nil {
		// May fail, but again, we'll do our best to cleanup here.
		os.Remove(zipFile.Name())
		return "", err
	}

	return zipFile.Name(), nil
}

// excludeDirEntryCondition resolves when a file or directory should be considered or not as part of build, when build is a
// copy-paste source strategy. Return true to exclude the directory entry.
type excludeDirEntryCondition func(path string, file os.FileInfo) bool

// buildForZipOptions provides a set of options for doing build for zip
type buildForZipOptions struct {
	excludeConditions []excludeDirEntryCondition
}

// buildForZip is used by projects whose build strategy is to only copy the source code into a folder, which is later
// zipped for packaging. buildForZipOptions provides the specific details for each language regarding which files should
// not be copied.
func buildForZip(src, dst string, options buildForZipOptions) error {
	// Add a global exclude condition for the .dotignore file
	ignoreMatchers, err := dotignore.ReadIgnoreFiles(src)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading .dotignore files: %w", err)
	}

	options.excludeConditions = append(options.excludeConditions, func(path string, file os.FileInfo) bool {
		if len(ignoreMatchers) > 0 {
			// Get the relative path from the source directory
			relativePath, err := filepath.Rel(src, path)
			if err != nil {
				return false
			}

			// Check if the relative path should be ignored
			isDir := file.IsDir()
			if dotignore.ShouldIgnore(relativePath, isDir, ignoreMatchers) {
				return true
			}
		}

		// Always exclude .dotignore files
		if filepath.Base(path) == ".dotignore" {
			return true
		}

		return false
	})

	// These exclude conditions apply to all projects
	options.excludeConditions = append(options.excludeConditions, globalExcludeAzdFolder)

	return copy.Copy(src, dst, copy.Options{
		Skip: func(srcInfo os.FileInfo, src, dest string) (bool, error) {
			for _, checkExclude := range options.excludeConditions {
				if checkExclude(src, srcInfo) {
					return true, nil
				}
			}
			return false, nil
		},
	})
}

func globalExcludeAzdFolder(path string, file os.FileInfo) bool {
	return file.IsDir() && file.Name() == ".azure"
}
