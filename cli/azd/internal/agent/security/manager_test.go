// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package security

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// platformSpecificPath returns a platform-appropriate path for testing security boundaries
func platformSpecificPath(pathType string) string {
	if runtime.GOOS == "windows" {
		switch pathType {
		case "hosts":
			return `C:\Windows\System32\drivers\etc\hosts`
		case "system":
			return `C:\Windows\System32\config\SAM`
		case "users_dir":
			return `C:\Users\Administrator\Desktop\secrets.txt`
		case "temp_file":
			return `C:\Windows\Temp\malicious.txt`
		case "ssh_keys":
			return `C:\Users\Administrator\.ssh\id_rsa`
		default:
			return `C:\Windows\System32\config\SAM`
		}
	}

	// Unix-like systems (Linux/macOS)
	switch pathType {
	case "hosts":
		return "/etc/hosts"
	case "system":
		return "/etc/passwd"
	case "users_dir":
		return "/home/user/secrets.txt"
	case "temp_file":
		return "/tmp/malicious.txt"
	case "ssh_keys":
		return "/root/.ssh/id_rsa"
	default:
		return "/etc/passwd"
	}
}

// outsidePath returns an absolute path that will be outside the test security root
// `kind` can be "system", "hosts", "tmp", or "ssh"; fallbacks provided
func outsidePath(kind string) string {
	if runtime.GOOS == "windows" {
		switch kind {
		case "hosts":
			return platformSpecificPath("hosts")
		case "tmp":
			return platformSpecificPath("temp_file")
		case "ssh":
			return platformSpecificPath("users_dir")
		default:
			return platformSpecificPath("system")
		}
	}

	// Unix-like defaults (Linux/macOS)
	switch kind {
	case "hosts":
		return platformSpecificPath("hosts")
	case "ssh":
		return platformSpecificPath("ssh_keys")
	case "tmp":
		return platformSpecificPath("temp_file")
	default:
		return platformSpecificPath("system")
	}
}

func TestSecurityManager_ValidatePath(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

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
		outsidePath("system"),
		outsidePath("hosts"),
	}

	for _, path := range invalidPaths {
		_, err := sm.ValidatePath(path)
		if err == nil {
			t.Errorf("Expected invalid path %s to fail validation, but it passed", path)
		}
	}
}

func TestSecurityManager_ValidatePath_DirectoryChange(t *testing.T) {
	// Create a temporary directory structure for testing
	tempDir := t.TempDir()

	subDir := filepath.Join(tempDir, "subdir")
	err := os.Mkdir(subDir, 0755)
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
	validated, err := sm.ValidatePath(subDirPath)
	if err != nil {
		t.Errorf("Expected valid directory change to %s to pass, got error: %v", subDirPath, err)
	}
	if validated == "" {
		t.Errorf("Expected validated path for %s, got empty string", subDirPath)
	}

	// Test invalid directory changes outside security root
	_, err = sm.ValidatePath("..")
	if err == nil {
		t.Error("Expected directory change to .. to fail validation, but it passed")
	}

	_, err = sm.ValidatePath("../../")
	if err == nil {
		t.Error("Expected directory change to ../../ to fail validation, but it passed")
	}
}

func TestResolvePath_ExistingFiles(t *testing.T) {
	// Create a test directory structure
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")

	// Create the test file
	if err := os.WriteFile(testFile, []byte("test content"), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test resolving an existing file
	resolved, err := resolvePath(testFile)
	if err != nil {
		t.Errorf("resolvePath failed for existing file: %v", err)
	}

	// Should return absolute canonical path
	if !filepath.IsAbs(resolved) {
		t.Errorf("Expected absolute path, got: %s", resolved)
	}

	// Test with relative path to existing file
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)

	os.Chdir(tempDir)
	resolved, err = resolvePath("test.txt")
	if err != nil {
		t.Errorf("resolvePath failed for relative path to existing file: %v", err)
	}

	if !filepath.IsAbs(resolved) {
		t.Errorf("Expected absolute path for relative input, got: %s", resolved)
	}
}

func TestResolvePath_NonExistentFiles(t *testing.T) {
	// Create a test directory structure
	tempDir := t.TempDir()

	// Test case 1: Non-existent file in existing directory
	nonExistentFile := filepath.Join(tempDir, "nonexistent.txt")
	resolved, err := resolvePath(nonExistentFile)
	if err != nil {
		t.Errorf("resolvePath failed for non-existent file in existing dir: %v", err)
	}

	// Should resolve the directory part and append the filename
	expectedDir, err := filepath.EvalSymlinks(tempDir)
	if err != nil {
		t.Fatalf("Failed to resolve temp dir: %v", err)
	}
	expected := filepath.Join(expectedDir, "nonexistent.txt")
	if resolved != expected {
		t.Errorf("Expected %s, got %s", expected, resolved)
	}
}

func TestResolvePath_DeepNonExistentPaths(t *testing.T) {
	// Create a test directory structure with some existing and some non-existing parts
	tempDir := t.TempDir()

	// Create: tempDir/level1/level2/
	level1 := filepath.Join(tempDir, "level1")
	level2 := filepath.Join(level1, "level2")
	if err := os.MkdirAll(level2, 0755); err != nil {
		t.Fatalf("Failed to create test directories: %v", err)
	}

	// Test case 1: Existing path with non-existent file
	// tempDir/level1/level2/nonexistent.txt
	deepNonExistentFile := filepath.Join(level2, "nonexistent.txt")
	resolved, err := resolvePath(deepNonExistentFile)
	if err != nil {
		t.Errorf("resolvePath failed for deep non-existent file: %v", err)
	}

	resolvedLevel2, err := filepath.EvalSymlinks(level2)
	if err != nil {
		t.Fatalf("Failed to resolve level2 dir: %v", err)
	}
	expected := filepath.Join(resolvedLevel2, "nonexistent.txt")
	if resolved != expected {
		t.Errorf("Expected %s, got %s", expected, resolved)
	}

	// Test case 2: Partially non-existent path
	// tempDir/level1/level2/level3/level4/nonexistent.txt
	deepNonExistentPath := filepath.Join(level2, "level3", "level4", "nonexistent.txt")
	resolved, err = resolvePath(deepNonExistentPath)
	if err != nil {
		t.Errorf("resolvePath failed for partially non-existent path: %v", err)
	}

	// Should resolve up to level2 and append the rest
	expected = filepath.Join(resolvedLevel2, "level3", "level4", "nonexistent.txt")
	if resolved != expected {
		t.Errorf("Expected %s, got %s", expected, resolved)
	}
}

func TestResolvePartialPath_Recursive(t *testing.T) {
	// Create a test directory structure
	tempDir := t.TempDir()

	// Create: tempDir/existing/
	existingDir := filepath.Join(tempDir, "existing")
	if err := os.Mkdir(existingDir, 0755); err != nil {
		t.Fatalf("Failed to create existing directory: %v", err)
	}

	// Test case 1: Multiple non-existent levels
	// tempDir/existing/nonexistent1/nonexistent2/file.txt
	deepPath := filepath.Join(existingDir, "nonexistent1", "nonexistent2", "file.txt")
	resolved := resolvePartialPath(deepPath)

	// Should resolve existingDir and append the non-existent parts
	resolvedExisting, err := filepath.EvalSymlinks(existingDir)
	if err != nil {
		t.Fatalf("Failed to resolve existing dir: %v", err)
	}
	expected := filepath.Join(resolvedExisting, "nonexistent1", "nonexistent2", "file.txt")
	if resolved != expected {
		t.Errorf("Expected %s, got %s", expected, resolved)
	}

	// Test case 2: All components exist
	existingFile := filepath.Join(existingDir, "realfile.txt")
	if err := os.WriteFile(existingFile, []byte("test"), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// This should fall back to EvalSymlinks working correctly
	resolved = resolvePartialPath(existingFile)
	resolvedFile, err := filepath.EvalSymlinks(existingFile)
	if err != nil {
		t.Fatalf("Failed to resolve existing file: %v", err)
	}
	if resolved != resolvedFile {
		t.Errorf("Expected %s, got %s", resolvedFile, resolved)
	}
}

func TestResolvePath_Integration_WithSecurityManager(t *testing.T) {
	// Create test directory structure
	tempDir := t.TempDir()

	// Create security manager
	sm, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("Failed to create security manager: %v", err)
	}

	// Test case 1: Non-existent file within security root should be allowed
	nonExistentInRoot := filepath.Join(tempDir, "future_file.txt")
	resolved, err := sm.ValidatePath(nonExistentInRoot)
	if err != nil {
		t.Errorf("Expected non-existent file within root to be allowed: %v", err)
	}
	if resolved == "" {
		t.Error("Expected resolved path to be returned")
	}

	// Test case 2: Deep non-existent path within security root should be allowed
	deepPath := filepath.Join(tempDir, "deep", "nested", "future_file.txt")
	resolved, err = sm.ValidatePath(deepPath)
	if err != nil {
		t.Errorf("Expected deep non-existent path within root to be allowed: %v", err)
	}
	if resolved == "" {
		t.Error("Expected resolved path to be returned")
	}

	// Test case 3: Non-existent file outside security root should be denied
	outsideFile := filepath.Join(filepath.Dir(tempDir), "outside.txt")
	_, err = sm.ValidatePath(outsideFile)
	if err == nil {
		t.Error("Expected non-existent file outside root to be denied")
	}
}
