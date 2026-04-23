// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

	cleanedBase := filepath.Clean(basePath)
	cleanedTarget := filepath.Clean(normalizedTarget)

	// Handle exact match (target IS the base directory).
	if pathEqual(cleanedTarget, cleanedBase) {
		return true
	}

	// Build prefix for containment check.
	// filepath.Clean on root paths (e.g., "/" or "C:\") retains the trailing separator,
	// so only add one if not already present to avoid a "//"-prefix false negative.
	basePrefix := cleanedBase
	if !strings.HasSuffix(basePrefix, string(os.PathSeparator)) {
		basePrefix += string(os.PathSeparator)
	}

	return pathHasPrefix(cleanedTarget, basePrefix)
}

// pathEqual compares two cleaned file paths. On Windows, paths are compared
// case-insensitively to match the filesystem's behavior.
func pathEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// pathHasPrefix checks if target starts with prefix. On Windows, the comparison
// is case-insensitive to match the filesystem's behavior.
func pathHasPrefix(target, prefix string) bool {
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(target), strings.ToLower(prefix))
	}
	return strings.HasPrefix(target, prefix)
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
