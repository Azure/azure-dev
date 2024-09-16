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

// createDeployableZip creates a zip file of a folder.
func createDeployableZip(projectName string, appName string, path string) (string, error) {
	// Create the output zip file path
	filePath := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%s-azddeploy-%d.zip", projectName, appName, time.Now().Unix()))
	zipFile, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to create zip file: %w", err)
	}

	// Zip the directory without any exclusions (they've already been handled in buildForZip)
	err = rzip.CreateFromDirectory(path, zipFile)
	if err != nil {
		zipFile.Close()
		os.Remove(zipFile.Name())
		return "", err
	}

	// Close the zip file and return the path
	if err := zipFile.Close(); err != nil {
		os.Remove(zipFile.Name())
		return "", err
	}

	return filePath, nil
}

// excludeDirEntryCondition resolves when a file or directory should be considered or not as part of build, when build is a
// copy-paste source strategy. Return true to exclude the directory entry.
type excludeDirEntryCondition func(path string, file os.FileInfo) bool

// buildForZipOptions provides a set of options for doing build for zip
type buildForZipOptions struct {
	excludeConditions []excludeDirEntryCondition
}

// buildForZip is used by projects to prepare a directory for
// zipping, excluding files based on the ignore file and other conditions.
func buildForZip(src, dst string, options buildForZipOptions, serviceConfig *ServiceConfig) error {
	// Lookup the appropriate ignore file name based on the service kind (Host)
	ignoreFileName := GetIgnoreFileNameByKind(serviceConfig.Host)

	// Read and honor the specified ignore file if it exists
	ignoreMatcher, err := dotignore.ReadDotIgnoreFile(src, ignoreFileName)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s file: %w", ignoreFileName, err)
	}

	// Temporary array to build exclude conditions dynamically
	tempExcludeConditions := []excludeDirEntryCondition{}

	// If there's no .ignore file, add the provided excludeConditions
	if ignoreMatcher == nil {
		tempExcludeConditions = append(tempExcludeConditions, options.excludeConditions...)
	} else {
		// If there's a .ignore file, apply ignoreMatcher only
		tempExcludeConditions = append(tempExcludeConditions, func(path string, file os.FileInfo) bool {
			relativePath, err := filepath.Rel(src, path)
			if err == nil && dotignore.ShouldIgnore(relativePath, file.IsDir(), ignoreMatcher) {
				return true
			}
			return false
		})
	}

	// Always append the global exclusions (e.g., .azure folder)
	tempExcludeConditions = append(tempExcludeConditions, globalExcludeAzdFolder)

	// Copy the source directory to the destination, applying the final exclude conditions
	return copy.Copy(src, dst, copy.Options{
		Skip: func(srcInfo os.FileInfo, src, dest string) (bool, error) {
			// Apply exclude conditions (either the default or the ignoreMatcher)
			for _, checkExclude := range tempExcludeConditions {
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
