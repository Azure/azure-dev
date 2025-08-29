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

	return &Manager{
		securityRoot: cleanRoot,
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

	// Check if the path is within the security root
	if !sm.isWithinSecurityRoot(cleanPath) {
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
	// Ensure both paths are absolute and cleaned
	absSecurityRoot, err := filepath.Abs(sm.securityRoot)
	if err != nil {
		return false
	}
	absSecurityRoot = filepath.Clean(absSecurityRoot)

	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absPath = filepath.Clean(absPath)

	// Calculate relative path from security root to the target path
	relPath, err := filepath.Rel(absSecurityRoot, absPath)
	if err != nil {
		return false
	}

	// Normalize path separators for cross-platform compatibility
	relPath = filepath.ToSlash(relPath)

	// Check if path is within security root:
	// - Should not start with "../" (going up and out)
	// - Should not be exactly ".." (parent directory)
	// - Should not start with "/" (absolute path, which shouldn't happen after Rel)
	result := !strings.HasPrefix(relPath, "../") &&
		relPath != ".." &&
		!strings.HasPrefix(relPath, "/")

	return result
}
