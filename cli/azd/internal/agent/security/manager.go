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
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return sm.securityRoot
}

// ValidatePath validates that the given path is within the security boundary
func (sm *Manager) ValidatePath(inputPath string) (string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

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

	fmt.Fprintf(os.Stderr, "[DEBUG] Prefix check - SecurityRoot: %q, Path: %q\n", sm.securityRoot, resolvedPath)

	// Verify prefix relationship - the path should start with the security root
	// Add separator to both to ensure we're checking directory boundaries, not just string prefixes
	// This prevents "/tmp/safe" from being considered within "/tmp/saf"
	result := strings.HasPrefix(resolvedPath+string(filepath.Separator), sm.securityRoot+string(filepath.Separator))
	if !result {
		fmt.Fprintf(os.Stderr, "[DEBUG] Validation FAILED - Path %q is outside security root %q\n", resolvedPath, sm.securityRoot)
		return "", fmt.Errorf("access denied: path outside allowed directory")
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Validation PASSED - Path %q is within security root %q\n", resolvedPath, sm.securityRoot)
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
		// If file doesn't exist, resolve directory portion for consistency
		dir := filepath.Dir(absPath)
		filename := filepath.Base(absPath)

		if resolvedDir, dirErr := filepath.EvalSymlinks(dir); dirErr == nil {
			resolvedPath = filepath.Join(resolvedDir, filename)
		} else {
			// Fallback to absolute path if directory resolution fails
			resolvedPath = absPath
		}
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] ResolvePath - Input: %q, Resolved: %q\n", inputPath, resolvedPath)
	return resolvedPath, nil
}
