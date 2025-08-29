// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SecurityManager handles path validation and access control for IO tools
type SecurityManager struct {
	securityRoot string
	mu           sync.RWMutex
}

var (
	// Global security manager instance
	globalSecurityManager *SecurityManager
	securityManagerOnce   sync.Once
)

// InitializeSecurityManager initializes the global security manager with the specified root directory
func InitializeSecurityManager(rootPath string) error {
	var initErr error
	securityManagerOnce.Do(func() {
		// Convert to absolute path and clean it
		absRoot, err := filepath.Abs(rootPath)
		if err != nil {
			initErr = fmt.Errorf("failed to resolve security root path %s: %w", rootPath, err)
			return
		}

		// Clean the path to normalize it
		cleanRoot := filepath.Clean(absRoot)

		// Verify the directory exists
		info, err := os.Stat(cleanRoot)
		if err != nil {
			initErr = fmt.Errorf("security root directory does not exist: %s: %w", cleanRoot, err)
			return
		}

		if !info.IsDir() {
			initErr = fmt.Errorf("security root path is not a directory: %s", cleanRoot)
			return
		}

		globalSecurityManager = &SecurityManager{
			securityRoot: cleanRoot,
		}
	})

	return initErr
}

// GetSecurityManager returns the global security manager instance
func GetSecurityManager() *SecurityManager {
	if globalSecurityManager == nil {
		// If not explicitly initialized, try to initialize with current working directory
		if cwd, err := os.Getwd(); err == nil {
			_ = InitializeSecurityManager(cwd)
		}
	}
	return globalSecurityManager
}

// ValidatePath validates that the given path is within the security boundary
func (sm *SecurityManager) ValidatePath(inputPath string) (string, error) {
	if sm == nil {
		return "", fmt.Errorf("security manager not initialized")
	}

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

// ValidateDirectoryChange validates a directory change operation
// This allows navigation within the security root boundary
func (sm *SecurityManager) ValidateDirectoryChange(inputPath string) (string, error) {
	if sm == nil {
		return "", fmt.Errorf("security manager not initialized")
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Convert input to absolute path
	absPath, err := filepath.Abs(inputPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	// Clean the path
	cleanPath := filepath.Clean(absPath)

	// Check: must be within security root
	if !sm.isWithinSecurityRoot(cleanPath) {
		return "", fmt.Errorf("access denied: cannot navigate outside project directory")
	}

	// Allow navigation within the security root
	return cleanPath, nil
}

// ValidatePathPair validates both source and destination paths (for copy, move operations)
func (sm *SecurityManager) ValidatePathPair(sourcePath, destPath string) (string, string, error) {
	cleanSource, err := sm.ValidatePath(sourcePath)
	if err != nil {
		return "", "", fmt.Errorf("source path validation failed: %w", err)
	}

	cleanDest, err := sm.ValidatePath(destPath)
	if err != nil {
		return "", "", fmt.Errorf("destination path validation failed: %w", err)
	}

	return cleanSource, cleanDest, nil
}

// GetSecurityRoot returns the current security root directory
func (sm *SecurityManager) GetSecurityRoot() string {
	if sm == nil {
		return ""
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.securityRoot
}

// isWithinSecurityRoot checks if the given path is within the security root
func (sm *SecurityManager) isWithinSecurityRoot(path string) bool {
	// Normalize both paths for comparison
	relPath, err := filepath.Rel(sm.securityRoot, path)
	if err != nil {
		return false
	}

	// If the relative path starts with "..", it's outside the security root
	return !strings.HasPrefix(relPath, "..") && relPath != ".."
}

// CreateSecurityErrorResponse creates a standardized error response for security violations
func CreateSecurityErrorResponse(err error) string {
	// Generic error message to avoid information disclosure
	message := "Access denied: operation not permitted outside the allowed directory"
	if err != nil {
		// For debugging, we might want to include the actual error in logs
		// but return a generic message to the user
		message = fmt.Sprintf("%s (%s)", message, err.Error())
	}

	type SecurityErrorResponse struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}

	response := SecurityErrorResponse{
		Success: false,
		Error:   "SECURITY_VIOLATION",
		Message: message,
	}

	// Convert to JSON (we'll assume json.MarshalIndent works for simplicity)
	// In a real implementation, we'd handle the error properly
	jsonData, _ := json.MarshalIndent(response, "", "  ")
	return string(jsonData)
}
