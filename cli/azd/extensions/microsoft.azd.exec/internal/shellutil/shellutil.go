// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package shellutil provides shell detection and validation utilities for the CLI executor.
package shellutil

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
// An empty string is considered valid (auto-detect). Returns an error for
// unknown shells, listing the valid options.
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
// file based on its extension. When the extension is unrecognized it falls
// back to the platform default (powershell on Windows, bash elsewhere).
//
// Note: The SDK's [azdext.DetectShell] detects the *current* interactive shell;
// this function detects shell from a *file extension*, which is a different concern.
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

// DefaultShell returns the platform-appropriate default shell
// (powershell on Windows, bash elsewhere).
func DefaultShell() string {
	if runtime.GOOS == osWindows {
		return "powershell"
	}
	return "bash"
}
