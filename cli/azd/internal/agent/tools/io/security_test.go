// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSecurityManager_ValidatePath(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Initialize security manager with temp directory as root
	err := InitializeSecurityManager(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize security manager: %v", err)
	}

	sm := GetSecurityManager()
	if sm == nil {
		t.Fatal("Security manager is nil")
	}

	// Test valid paths within security root
	validPaths := []string{
		".",
		"./subdir",
		"file.txt",
		"subdir/file.txt",
	}

	for _, path := range validPaths {
		testPath := filepath.Join(tempDir, path)
		validated, err := sm.ValidatePath(testPath)
		if err != nil {
			t.Errorf("Expected valid path %s to pass validation, got error: %v", path, err)
		}
		if validated == "" {
			t.Errorf("Expected validated path for %s, got empty string", path)
		}
	}

	// Test invalid paths outside security root
	invalidPaths := []string{
		"..",
		"../outside",
		"../../escape",
		platformSpecificPath("users_dir"), // Unix: /etc/passwd, Windows: C:\Users\Administrator\Desktop\secrets.txt
		platformSpecificPath("system"),    // Unix: /etc/passwd, Windows: C:\Windows\System32
	}

	for _, path := range invalidPaths {
		_, err := sm.ValidatePath(path)
		if err == nil {
			t.Errorf("Expected invalid path %s to fail validation, but it passed", path)
		}
	}
}

func TestSecurityManager_ValidateDirectoryChange(t *testing.T) {
	// Reset global state to ensure clean test
	globalSecurityManager = nil
	securityManagerOnce = sync.Once{}

	// Create a temporary directory structure for testing
	tempDir := t.TempDir()

	subDir := filepath.Join(tempDir, "subdir")
	err := os.Mkdir(subDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	// Initialize security manager with temp directory as root
	err = InitializeSecurityManager(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize security manager: %v", err)
	}

	// Change to temp directory first
	originalWd, _ := os.Getwd()
	defer os.Chdir(originalWd)
	os.Chdir(tempDir)

	sm := GetSecurityManager()

	// Test valid directory changes within security root
	subDirPath := filepath.Join(tempDir, "subdir")
	validated, err := sm.ValidateDirectoryChange(subDirPath)
	if err != nil {
		t.Errorf("Expected valid directory change to %s to pass, got error: %v", subDirPath, err)
	}
	if validated == "" {
		t.Errorf("Expected validated path for %s, got empty string", subDirPath)
	}

	// Test invalid directory changes outside security root
	_, err = sm.ValidateDirectoryChange("..")
	if err == nil {
		t.Error("Expected directory change to .. to fail validation, but it passed")
	}

	_, err = sm.ValidateDirectoryChange("../../")
	if err == nil {
		t.Error("Expected directory change to ../../ to fail validation, but it passed")
	}
}
