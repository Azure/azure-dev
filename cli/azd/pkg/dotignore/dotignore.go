// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dotignore

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/denormal/go-gitignore"
)

// ReadDotIgnoreFile reads the ignore file located at the root of the project directory.
// If the ignoreFileName is blank or the file is not found, it returns nil, nil.
func ReadDotIgnoreFile(projectDir string, ignoreFileName string) (gitignore.GitIgnore, error) {
	// Return nil if the ignoreFileName is empty
	if ignoreFileName == "" {
		return nil, nil
	}

	ignoreFilePath := filepath.Join(projectDir, ignoreFileName)
	if _, err := os.Stat(ignoreFilePath); os.IsNotExist(err) {
		// Return nil if the ignore file does not exist
		return nil, nil
	}

	ignoreMatcher, err := gitignore.NewFromFile(ignoreFilePath)
	if err != nil {
		return nil, fmt.Errorf("error reading %s file at %s: %w", ignoreFileName, ignoreFilePath, err)
	}

	return ignoreMatcher, nil
}

// ShouldIgnore determines whether a file or directory should be ignored based on the provided ignore matcher.
func ShouldIgnore(relativePath string, isDir bool, ignoreMatcher gitignore.GitIgnore) bool {
	if ignoreMatcher != nil {
		if match := ignoreMatcher.Relative(relativePath, isDir); match != nil && match.Ignore() {
			return true
		}
	}
	return false
}
