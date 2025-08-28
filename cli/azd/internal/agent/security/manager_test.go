// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecurityManager_ValidatePath(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "security_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create security manager directly with temp directory as root
	sm, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("Failed to create security manager: %v", err)
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
		"/etc/passwd",
		"C:\\Windows\\System32",
	}

	for _, path := range invalidPaths {
		_, err := sm.ValidatePath(path)
		if err == nil {
			t.Errorf("Expected invalid path %s to fail validation, but it passed", path)
		}
	}
}

func TestSecurityManager_ValidateDirectoryChange(t *testing.T) {
	// Create a temporary directory structure for testing
	tempDir, err := os.MkdirTemp("", "security_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	subDir := filepath.Join(tempDir, "subdir")
	err = os.Mkdir(subDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	// Create security manager directly with temp directory as root
	sm, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("Failed to create security manager: %v", err)
	}

	// Change to temp directory first
	originalWd, _ := os.Getwd()
	defer os.Chdir(originalWd)
	os.Chdir(tempDir)

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
