// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsPathContained checks whether targetPath is contained within basePath after
// cleaning both paths. This is used to prevent path traversal attacks where a
// resolved path might escape its intended directory.
// Backslashes in targetPath are normalized to the OS separator before checking,
// so paths like "..\..\malicious" are caught on all platforms.
func IsPathContained(basePath, targetPath string) bool {
	// Normalize backslashes to OS separator so traversal via "..\.." is caught on Linux too.
	normalizedTarget := strings.ReplaceAll(targetPath, "\\", string(os.PathSeparator))

	cleanedBase := filepath.Clean(basePath) + string(os.PathSeparator)
	cleanedTarget := filepath.Clean(normalizedTarget)

	// Also handle the case where target IS exactly the base directory
	if cleanedTarget == filepath.Clean(basePath) {
		return true
	}

	return strings.HasPrefix(cleanedTarget, cleanedBase)
}

// ResolveContainedPath attempts to resolve filePath within one of the given root directories.
// It returns the first absolute path that exists and is contained within its root.
// If the path resolves outside all roots, it returns a path-traversal error.
// If the path is contained but does not exist in any root, it returns a not-found error.
func ResolveContainedPath(roots []string, filePath string) (string, error) {
	pathBlocked := false

	for _, root := range roots {
		absolutePath := filepath.Join(root, filePath)

		absolutePath, err := filepath.Abs(absolutePath)
		if err != nil {
			return "", err
		}

		if !IsPathContained(root, absolutePath) {
			pathBlocked = true
			continue
		}

		if _, err := os.Stat(absolutePath); err == nil {
			return absolutePath, nil
		}
	}

	if pathBlocked {
		return "", fmt.Errorf(
			"path %q resolves outside all root directories; "+
				"use an absolute path or a path within the root directory", filePath)
	}

	return "", fmt.Errorf("file '%s' was not found: %w", filePath, os.ErrNotExist)
}
