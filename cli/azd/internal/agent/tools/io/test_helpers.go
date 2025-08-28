// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/agent/security"
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
	tempDir, err := os.MkdirTemp("", "io_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

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

// outsidePath returns an absolute path that will be outside the test security root
// `kind` can be "system", "hosts", "startup", "tmp", or "ssh"; fallbacks provided
func outsidePath(kind string) string {
	if runtime.GOOS == "windows" {
		switch kind {
		case "hosts":
			return `C:\\Windows\\System32\\drivers\\etc\\hosts`
		case "startup":
			return filepath.Join("C:\\", "ProgramData", "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "malware.exe")
		case "ssh":
			return `C:\\Users\\Administrator\\Desktop\\secrets.txt`
		case "tmp":
			return filepath.Join("C:\\", "Windows", "Temp", "malicious.txt")
		default:
			return `C:\\Windows\\System32\\config\\SAM`
		}
	}

	// Unix-like defaults (Linux/macOS)
	switch kind {
	case "hosts":
		return "/etc/hosts"
	case "ssh":
		return "/root/.ssh/id_rsa"
	case "tmp":
		return "/tmp/malicious.txt"
	case "startup":
		return "/tmp/malware.exe"
	default:
		return "/etc/passwd"
	}
}
