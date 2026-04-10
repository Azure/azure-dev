// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package scripting provides secure script and command execution with Azure context.
package scripting

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

const osWindows = "windows"

// ValidShells is the canonical set of supported shell names (lowercase).
var ValidShells = map[string]bool{
	"bash":       true,
	"sh":         true,
	"zsh":        true,
	"pwsh":       true,
	"powershell": true,
	"cmd":        true,
}

// ValidateShell checks whether shell is a known, supported shell name.
// An empty string is considered valid (auto-detect).
func ValidateShell(shell string) error {
	if shell == "" {
		return nil
	}
	if !ValidShells[strings.ToLower(shell)] {
		return fmt.Errorf("invalid shell %q: must be one of bash, sh, zsh, pwsh, powershell, cmd", shell)
	}
	return nil
}

// DetectShellFromFile returns the appropriate shell for executing a script
// file based on its extension.
func DetectShellFromFile(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".sh", ".bash":
		return "bash"
	case ".zsh":
		return "zsh"
	case ".ps1":
		return "pwsh"
	case ".cmd", ".bat":
		return "cmd"
	default:
		return DefaultShell()
	}
}

// DefaultShell returns the platform-appropriate default shell.
func DefaultShell() string {
	if runtime.GOOS == osWindows {
		return "powershell"
	}
	return "bash"
}