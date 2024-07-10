// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"fmt"
	"os"
)

// DirExists checks if the given directory path exists.
// It returns true if the directory exists, false otherwise.
func DirExists(dirPath string) bool {
	if _, err := os.Stat(dirPath); err == nil {
		return true
	}
	return false
}

// FileExists checks if the given file path exists and is a regular file.
// It returns true if the file exists and is regular, false otherwise.
func FileExists(filePath string) bool {
	info, err := os.Stat(filePath)
	if err == nil && info.Mode().IsRegular() {
		return true
	}
	return false
}

// IsDirEmpty checks if the given directory is empty.
// If the directory does not exist, it can either treat it as an error or as an empty directory based on the input flag.
// By default, it treats a missing directory as an error.
func IsDirEmpty(directoryPath string, treatMissingAsEmpty ...bool) (bool, error) {
	// Default behavior: treat a missing directory as an error
	treatAsEmpty := false

	// Check if the caller has provided the optional parameter
	if len(treatMissingAsEmpty) > 0 {
		// Use the value provided by the caller
		treatAsEmpty = treatMissingAsEmpty[0]
	}

	files, err := os.ReadDir(directoryPath)
	if err != nil {
		// If the directory does not exist and we should treat it as empty
		if os.IsNotExist(err) && treatAsEmpty {
			return true, nil
		}
		// Otherwise, return the error
		return false, fmt.Errorf("determining empty directory: %w", err)
	}
	return len(files) == 0, nil
}
