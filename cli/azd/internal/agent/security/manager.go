// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Manager handles path validation and access control for agent tools
type Manager struct {
	securityRoot string
}

// NewManager creates a new security manager with the specified root directory
func NewManager(rootPath string) (*Manager, error) {
	// Try to resolve symlinks and canonical paths first (handles Unix symlinks and Windows short names)
	resolvedRoot, err := resolvePath(rootPath)
	if err != nil {
		return nil, err
	}

	// Verify the directory exists
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		return nil, fmt.Errorf("security root directory does not exist: %s: %w", resolvedRoot, err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("security root path is not a directory: %s", resolvedRoot)
	}

	return &Manager{
		securityRoot: resolvedRoot,
	}, nil
}

// GetSecurityRoot returns the current security root directory
func (sm *Manager) GetSecurityRoot() string {
	return sm.securityRoot
}

// ValidatePath validates that the given path is within the security boundary
func (sm *Manager) ValidatePath(inputPath string) (string, error) {
	var resolvedPath string
	var err error

	if filepath.IsAbs(inputPath) {
		// Handle absolute paths - resolve them directly
		resolvedPath, err = resolvePath(inputPath)
		if err != nil {
			return "", err
		}
	} else {
		// Handle relative paths - use safeJoin approach
		resolvedPath, err = sm.safeJoin(inputPath)
		if err != nil {
			return "", err
		}
	}

	// Verify prefix relationship - the path should start with the security root
	// Add separator to both to ensure we're checking directory boundaries, not just string prefixes
	// This prevents "/tmp/safe" from being considered within "/tmp/saf"
	result := strings.HasPrefix(resolvedPath+string(filepath.Separator), sm.securityRoot+string(filepath.Separator))
	if !result {
		return "", fmt.Errorf("access denied: path outside allowed directory")
	}

	return resolvedPath, nil
}

// safeJoin safely joins the security root with a relative path and resolves it
func (sm *Manager) safeJoin(relativePath string) (string, error) {
	// Join with the security root
	joined := filepath.Join(sm.securityRoot, relativePath)

	// Resolve to canonical form
	return resolvePath(joined)
}

// resolvePath resolves a path to its canonical form, handling symlinks consistently
func resolvePath(inputPath string) (string, error) {
	// Convert to absolute path
	absPath, err := filepath.Abs(inputPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	// Try to resolve symlinks for consistent comparison
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// If file doesn't exist, recursively find the deepest existing part
		resolvedPath = resolvePartialPath(absPath)
	}

	return resolvedPath, nil
}

// resolvePartialPath recursively finds the deepest existing part of a path and resolves it
func resolvePartialPath(fullPath string) string {
	// Split the path into components to work backwards
	var pathComponents []string
	currentPath := fullPath

	// Collect all path components from the full path back to an existing directory
	for currentPath != filepath.Dir(currentPath) { // Stop when we reach root
		pathComponents = append([]string{filepath.Base(currentPath)}, pathComponents...)
		currentPath = filepath.Dir(currentPath)

		// Try to resolve this level
		if resolvedDir, err := filepath.EvalSymlinks(currentPath); err == nil {
			// Found an existing directory that we can resolve
			// Rebuild the path from the resolved existing part
			result := resolvedDir
			for _, component := range pathComponents {
				result = filepath.Join(result, component)
			}
			return result
		}
	}

	// If we get here, nothing could be resolved (shouldn't happen with absolute paths)
	// Fall back to the original path
	return fullPath
}
