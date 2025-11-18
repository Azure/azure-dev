// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/internal/agent/security"
)

// mustMarshalJSON is a test helper function to marshal request structs to JSON strings
func mustMarshalJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("Failed to marshal JSON: %v", err))
	}
	return string(data)
}

// createTestSecurityManager creates a SecurityManager for testing with a temporary directory
func createTestSecurityManager(t *testing.T) (*security.Manager, string) {
	tempDir := t.TempDir()

	// Change to the test directory
	originalWd, _ := os.Getwd()
	os.Chdir(tempDir)
	t.Cleanup(func() {
		os.Chdir(originalWd)
		os.RemoveAll(tempDir)
	})

	sm, err := security.NewManager(tempDir)
	if err != nil {
		t.Fatalf("Failed to create security manager: %v", err)
	}

	return sm, tempDir
}

// createTestTools creates all IO tools with proper SecurityManager injection for testing
func createTestTools(t *testing.T) (map[string]interface{}, string) {
	sm, tempDir := createTestSecurityManager(t)

	tools := map[string]interface{}{
		"read":       ReadFileTool{securityManager: sm},
		"write":      WriteFileTool{securityManager: sm},
		"copy":       CopyFileTool{securityManager: sm},
		"move":       MoveFileTool{securityManager: sm},
		"delete":     DeleteFileTool{securityManager: sm},
		"createDir":  CreateDirectoryTool{securityManager: sm},
		"deleteDir":  DeleteDirectoryTool{securityManager: sm},
		"listDir":    DirectoryListTool{securityManager: sm},
		"fileInfo":   FileInfoTool{securityManager: sm},
		"fileSearch": FileSearchTool{securityManager: sm},
		"changeDir":  ChangeDirectoryTool{securityManager: sm},
	}

	return tools, tempDir
}

// createTestReadTool creates a ReadFileTool with proper SecurityManager for unit testing
func createTestReadTool(t *testing.T) (ReadFileTool, string) {
	sm, tempDir := createTestSecurityManager(t)
	return ReadFileTool{securityManager: sm}, tempDir
}

// createTestWriteTool creates a WriteFileTool with proper SecurityManager for unit testing
func createTestWriteTool(t *testing.T) (WriteFileTool, string) {
	sm, tempDir := createTestSecurityManager(t)
	return WriteFileTool{securityManager: sm}, tempDir
}

// absoluteOutsidePath returns an absolute path that will be outside the test security root
// This is specifically for testing absolute path validation
func absoluteOutsidePath(kind string) string {
	if runtime.GOOS == "windows" {
		switch kind {
		case "root":
			return "C:\\"
		case "system":
			return "C:\\Windows\\System32"
		case "temp":
			return "C:\\Windows\\Temp"
		case "temp_dir":
			return "C:\\Windows\\Temp\\malicious_dir"
		case "users":
			return "C:\\Users"
		case "program_files":
			return "C:\\Program Files"
		default:
			return "C:\\Windows\\System32\\config\\SAM"
		}
	}

	// Unix-like systems
	switch kind {
	case "root":
		return "/"
	case "system":
		return "/etc"
	case "temp":
		return "/tmp"
	case "temp_dir":
		return "/tmp/malicious_dir"
	case "users":
		return "/home"
	case "program_files":
		return "/usr/bin"
	default:
		return "/etc/passwd"
	}
}

// relativeEscapePath returns a relative path that attempts to escape the security root
// using "../" patterns. This should be blocked by security validation.
func relativeEscapePath(kind string) string {
	switch kind {
	case "simple":
		return "../../../tmp"
	case "deep":
		return "../../../../../../../../etc/passwd"
	case "mixed":
		return "../../../tmp/malicious"
	case "with_file":
		return "../../../etc/passwd"
	case "with_dir":
		return "../../../tmp/"
	default:
		return "../../../tmp"
	}
}

// platformSpecificPath returns paths that are specific to the current platform
// This is useful for testing platform-specific security scenarios
func platformSpecificPath(kind string) string {
	if runtime.GOOS == "windows" {
		switch kind {
		case "startup_folder":
			return "C:\\ProgramData\\Microsoft\\Windows\\Start Menu\\Programs\\Startup\\malware.exe"
		case "system_drive":
			return "C:\\"
		case "windows_dir":
			return "C:\\Windows"
		case "program_files":
			return "C:\\Program Files\\malware.exe"
		case "users_dir":
			return "C:\\Users\\Administrator\\Desktop\\secrets.txt"
		default:
			return "C:\\Windows\\System32\\config\\SAM"
		}
	}

	// Unix-like systems
	switch kind {
	case "startup_folder":
		return "/etc/init.d/malware"
	case "system_drive":
		return "/"
	case "windows_dir":
		return "/etc"
	case "program_files":
		return "/usr/bin/malware"
	case "users_dir":
		return "/root/.ssh/id_rsa"
	default:
		return "/etc/passwd"
	}
}
