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

	// Debug logging for CI troubleshooting
	fmt.Fprintf(os.Stderr, "[DEBUG] Security validation - Input: %q, SecurityRoot: %q, CleanPath: %q\n",
		inputPath, sm.securityRoot, cleanPath)

	// Check if the path is within the security root
	if !sm.isWithinSecurityRoot(cleanPath) {
		fmt.Fprintf(os.Stderr, "[DEBUG] Security validation FAILED - Path %q is outside security root %q\n",
			cleanPath, sm.securityRoot)
		return "", fmt.Errorf("access denied: path outside allowed directory")
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Security validation PASSED - Path %q is within security root %q\n",
		cleanPath, sm.securityRoot)
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
		fmt.Fprintf(os.Stderr, "[DEBUG] Failed to get absolute path for security root %q: %v\n", sm.securityRoot, err)
		return false
	}
	absSecurityRoot = filepath.Clean(absSecurityRoot)

	absPath, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Failed to get absolute path for input %q: %v\n", path, err)
		return false
	}
	absPath = filepath.Clean(absPath)

	fmt.Fprintf(os.Stderr, "[DEBUG] Path comparison - AbsSecurityRoot: %q, AbsPath: %q\n", absSecurityRoot, absPath)

	// Resolve symlinks for both paths to ensure accurate comparison
	resolvedSecurityRoot, err := filepath.EvalSymlinks(absSecurityRoot)
	if err != nil {
		fmt.Fprintf(
			os.Stderr,
			"[DEBUG] Failed to resolve symlinks for security root %q: %v, using original\n",
			absSecurityRoot,
			err,
		)
		resolvedSecurityRoot = absSecurityRoot
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Failed to resolve symlinks for path %q: %v, using original\n", absPath, err)
		resolvedPath = absPath
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Resolved paths - SecurityRoot: %q, Path: %q\n", resolvedSecurityRoot, resolvedPath)

	// Calculate relative path from security root to the target path
	relPath, err := filepath.Rel(resolvedSecurityRoot, resolvedPath)
	if err != nil {
		fmt.Fprintf(
			os.Stderr,
			"[DEBUG] Failed to calculate relative path from %q to %q: %v\n",
			resolvedSecurityRoot,
			resolvedPath,
			err,
		)
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
