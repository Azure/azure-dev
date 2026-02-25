// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// PackageManagerKind represents the type of Node.js package manager.
type PackageManagerKind string

const (
	PackageManagerNpm  PackageManagerKind = "npm"
	PackageManagerPnpm PackageManagerKind = "pnpm"
	PackageManagerYarn PackageManagerKind = "yarn"
)

// DetectPackageManager determines the Node.js package manager to use for a given project directory.
// Detection priority (matches azd-app extension):
//  1. "packageManager" field in package.json (e.g., "pnpm@8.15.0")
//  2. Lock file presence: pnpm-lock.yaml → pnpm-workspace.yaml → yarn.lock → package-lock.json
//  3. Default: npm
func DetectPackageManager(projectDir string) PackageManagerKind {
	// 1. Check packageManager field in package.json
	if pm := getPackageManagerFromPackageJSON(projectDir); pm != "" {
		return pm
	}

	// 2. Check for lock files and workspace config
	if _, err := os.Stat(filepath.Join(projectDir, "pnpm-lock.yaml")); err == nil {
		return PackageManagerPnpm
	}
	if _, err := os.Stat(filepath.Join(projectDir, "pnpm-workspace.yaml")); err == nil {
		return PackageManagerPnpm
	}
	if _, err := os.Stat(filepath.Join(projectDir, "yarn.lock")); err == nil {
		return PackageManagerYarn
	}
	if _, err := os.Stat(filepath.Join(projectDir, "package-lock.json")); err == nil {
		return PackageManagerNpm
	}

	// 3. Default to npm
	return PackageManagerNpm
}

// getPackageManagerFromPackageJSON reads the "packageManager" field from package.json.
// The field format is "name@version" (e.g., "pnpm@8.15.0").
// Returns the package manager kind if found and supported, empty string otherwise.
func getPackageManagerFromPackageJSON(projectDir string) PackageManagerKind {
	data, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err != nil {
		return ""
	}

	var pkg struct {
		PackageManager string `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil || pkg.PackageManager == "" {
		return ""
	}

	// Extract name from "name@version" format
	name := strings.Split(pkg.PackageManager, "@")[0]
	switch name {
	case "pnpm":
		return PackageManagerPnpm
	case "yarn":
		return PackageManagerYarn
	case "npm":
		return PackageManagerNpm
	default:
		return ""
	}
}

// IsDependenciesUpToDate checks if node_modules is up-to-date relative to the lock file.
// This avoids unnecessary reinstalls when dependencies haven't changed.
// Pattern borrowed from azd-app extension's installer.
func IsDependenciesUpToDate(projectDir string, pm PackageManagerKind) bool {
	nodeModulesPath := filepath.Join(projectDir, "node_modules")

	// Determine which lock file and internal marker to check based on package manager
	var lockFile string
	var internalMarker string
	switch pm {
	case PackageManagerNpm:
		lockFile = "package-lock.json"
		internalMarker = filepath.Join("node_modules", ".package-lock.json")
	case PackageManagerPnpm:
		lockFile = "pnpm-lock.yaml"
		// pnpm uses a virtual store — check that .pnpm directory exists and is newer
		internalMarker = filepath.Join("node_modules", ".pnpm")
	case PackageManagerYarn:
		lockFile = "yarn.lock"
		// .yarn-integrity is created by both Classic (v1) and Berry (v2+) when using
		// node_modules linker. For PnP mode, node_modules won't exist so we return false above.
		internalMarker = filepath.Join("node_modules", ".yarn-integrity")
	default:
		return false
	}

	lockFilePath := filepath.Join(projectDir, lockFile)

	// Check if lock file exists
	lockFileInfo, err := os.Stat(lockFilePath)
	if err != nil {
		return false
	}

	// Check if node_modules exists
	if _, err := os.Stat(nodeModulesPath); err != nil {
		return false
	}

	// Compare internal marker timestamp against lock file
	markerPath := filepath.Join(projectDir, internalMarker)
	markerInfo, err := os.Stat(markerPath)
	if err != nil {
		return false
	}
	// If the internal marker is older than the lock file, dependencies are stale
	if markerInfo.ModTime().Before(lockFileInfo.ModTime()) {
		return false
	}

	return true
}
