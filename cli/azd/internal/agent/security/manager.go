// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Manager handles path validation and access control for agent tools
type Manager struct {
	securityRoot string
	mu           sync.RWMutex
}

// NewManager creates a new security manager with the specified root directory
func NewManager(rootPath string) (*Manager, error) {
	// Convert to absolute path and clean it
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve security root path %s: %w", rootPath, err)
	}

	// Clean the path to normalize it
	cleanRoot := filepath.Clean(absRoot)

	// Verify the directory exists
	info, err := os.Stat(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("security root directory does not exist: %s: %w", cleanRoot, err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("security root path is not a directory: %s", cleanRoot)
	}

	// Resolve symlinks for the security root to ensure consistent comparison
	resolvedRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		// If symlink resolution fails, use the clean path
		resolvedRoot = cleanRoot
	}

	return &Manager{
		securityRoot: resolvedRoot,
	}, nil
}

// ValidatePath validates that the given path is within the security boundary
func (sm *Manager) ValidatePath(inputPath string) (string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Convert to absolute path
	absPath, err := filepath.Abs(inputPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	// Clean the path to resolve any . or .. components
	cleanPath := filepath.Clean(absPath)

	// Resolve symlinks to prevent symlink attacks
	resolvedPath, err := filepath.EvalSymlinks(cleanPath)
	if err != nil {
		// If symlink resolution fails, check the original path
		// This handles cases where the symlink target doesn't exist yet
		resolvedPath = cleanPath
	}

	// Check if the resolved path is within the security root
	if !sm.isWithinSecurityRoot(resolvedPath) {
		return "", fmt.Errorf("access denied: path outside allowed directory")
	}

	return cleanPath, nil
}

// GetSecurityRoot returns the current security root directory
func (sm *Manager) GetSecurityRoot() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.securityRoot
}

// isWithinSecurityRoot checks if the given path is within the security root
func (sm *Manager) isWithinSecurityRoot(path string) bool {
	// Normalize both paths for comparison
	relPath, err := filepath.Rel(sm.securityRoot, path)
	if err != nil {
		return false
	}

	// If the relative path starts with "..", it's outside the security root
	return !strings.HasPrefix(relPath, "..") && relPath != ".."
}
