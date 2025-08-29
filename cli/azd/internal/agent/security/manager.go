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
	// Try to resolve symlinks and canonical paths first (handles Unix symlinks and Windows short names)
	resolvedRoot, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		// If EvalSymlinks fails (e.g., path doesn't exist), fall back to filepath.Abs
		resolvedRoot, err = filepath.Abs(rootPath)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve security root path %s: %w", rootPath, err)
		}
	} else {
		// EvalSymlinks returns absolute paths, but ensure it's absolute just in case
		resolvedRoot, err = filepath.Abs(resolvedRoot)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve security root path %s: %w", rootPath, err)
		}
	}
	absRoot := resolvedRoot

	// Verify the directory exists
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("security root directory does not exist: %s: %w", absRoot, err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("security root path is not a directory: %s", absRoot)
	}

	return &Manager{
		securityRoot: absRoot,
	}, nil
}

// ValidatePath validates that the given path is within the security boundary
func (sm *Manager) ValidatePath(inputPath string) (string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Try to resolve symlinks and canonical paths first (handles Unix symlinks and Windows short names)
	resolvedPath, err := filepath.EvalSymlinks(inputPath)
	if err != nil {
		// If EvalSymlinks fails (e.g., file doesn't exist), fall back to filepath.Abs
		resolvedPath, err = filepath.Abs(inputPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve path: %w", err)
		}
	} else {
		// EvalSymlinks returns absolute paths, but ensure it's absolute just in case
		resolvedPath, err = filepath.Abs(resolvedPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve path: %w", err)
		}
	}
	absPath := resolvedPath

	fmt.Fprintf(os.Stderr, "[DEBUG] Security validation - Input: %q, SecurityRoot: %q, AbsPath: %q\n",
		inputPath, sm.securityRoot, absPath)

	// Check if the path is within the security root
	if !sm.isWithinSecurityRoot(absPath) {
		fmt.Fprintf(os.Stderr, "[DEBUG] Security validation FAILED - Path %q is outside security root %q\n",
			absPath, sm.securityRoot)
		return "", fmt.Errorf("access denied: path outside allowed directory")
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Security validation PASSED - Path %q is within security root %q\n",
		absPath, sm.securityRoot)
	return absPath, nil
}

// GetSecurityRoot returns the current security root directory
func (sm *Manager) GetSecurityRoot() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.securityRoot
}

// isWithinSecurityRoot checks if the given path is within the security root
func (sm *Manager) isWithinSecurityRoot(path string) bool {
	fmt.Fprintf(os.Stderr, "[DEBUG] Raw inputs - sm.securityRoot: %q, path: %q\n", sm.securityRoot, path)

	// Both security root and input path are already processed with filepath.Abs()
	// No additional processing needed - just compare them directly

	fmt.Fprintf(os.Stderr, "[DEBUG] Final comparison - SecurityRoot: %q, AbsPath: %q\n", sm.securityRoot, path)

	// Calculate relative path from security root to the target path
	relPath, err := filepath.Rel(sm.securityRoot, path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Failed to calculate relative path from %q to %q: %v\n",
			sm.securityRoot, path, err)
		return false
	}

	// Normalize path separators for cross-platform compatibility
	relPath = filepath.ToSlash(relPath)

	fmt.Fprintf(os.Stderr, "[DEBUG] Relative path: %q\n", relPath)

	// Check if path is within security root:
	// - Should not start with "../" (going up and out)
	// - Should not be exactly ".." (parent directory)
	// - Should not start with "/" (absolute path, which shouldn't happen after Rel)
	result := !strings.HasPrefix(relPath, "../") &&
		relPath != ".." &&
		!strings.HasPrefix(relPath, "/")

	fmt.Fprintf(os.Stderr, "[DEBUG] Security check result: %t for relative path %q\n", result, relPath)
	return result
}
