// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package npm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDetectPackageManager_PackageJsonField(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		expected PackageManagerKind
	}{
		{"pnpm with version", `{"packageManager": "pnpm@8.15.0"}`, PackageManagerPnpm},
		{"yarn with version", `{"packageManager": "yarn@4.1.0"}`, PackageManagerYarn},
		{"npm with version", `{"packageManager": "npm@10.5.0"}`, PackageManagerNpm},
		{"unsupported pm", `{"packageManager": "bun@1.0.0"}`, PackageManagerNpm}, // falls through to default
		{"empty field", `{"packageManager": ""}`, PackageManagerNpm},             // falls through to default
		{"no field", `{}`, PackageManagerNpm},                                    // falls through to default
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(tt.field), 0600)
			require.NoError(t, err)

			result := DetectPackageManager(dir)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestDetectPackageManager_LockFiles(t *testing.T) {
	tests := []struct {
		name     string
		lockFile string
		expected PackageManagerKind
	}{
		{"pnpm-lock.yaml", "pnpm-lock.yaml", PackageManagerPnpm},
		{"yarn.lock", "yarn.lock", PackageManagerYarn},
		{"package-lock.json", "package-lock.json", PackageManagerNpm},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			// Write a package.json without packageManager field
			err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0600)
			require.NoError(t, err)
			// Write the lock file
			err = os.WriteFile(filepath.Join(dir, tt.lockFile), []byte(""), 0600)
			require.NoError(t, err)

			result := DetectPackageManager(dir)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestDetectPackageManager_PackageJsonFieldTakesPriority(t *testing.T) {
	dir := t.TempDir()

	// Write package.json with pnpm packageManager field
	err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"packageManager": "pnpm@8.15.0"}`), 0600)
	require.NoError(t, err)

	// Also write package-lock.json (npm lock file)
	err = os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte("{}"), 0600)
	require.NoError(t, err)

	// packageManager field should take priority
	result := DetectPackageManager(dir)
	require.Equal(t, PackageManagerPnpm, result)
}

func TestDetectPackageManager_NoPackageJson(t *testing.T) {
	dir := t.TempDir()
	// No package.json, no lock files — defaults to npm
	result := DetectPackageManager(dir)
	require.Equal(t, PackageManagerNpm, result)
}

func TestDetectPackageManager_DefaultsToNpm(t *testing.T) {
	dir := t.TempDir()
	// package.json without packageManager field, no lock files
	err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name": "test"}`), 0600)
	require.NoError(t, err)

	result := DetectPackageManager(dir)
	require.Equal(t, PackageManagerNpm, result)
}

func TestScriptExistsInPackageJSON(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		scriptName string
		expected   bool
	}{
		{"script exists", `{"scripts": {"build": "tsc"}}`, "build", true},
		{"script does not exist", `{"scripts": {"build": "tsc"}}`, "test", false},
		{"no scripts section", `{"name": "test"}`, "build", false},
		{"empty scripts", `{"scripts": {}}`, "build", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(tt.content), 0600)
			require.NoError(t, err)

			result, err := scriptExistsInPackageJSON(dir, tt.scriptName)
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestScriptExistsInPackageJSON_NoFile(t *testing.T) {
	dir := t.TempDir()
	// No package.json → false with no error (script definitively absent)
	result, err := scriptExistsInPackageJSON(dir, "build")
	require.NoError(t, err)
	require.False(t, result)
}

func TestScriptExistsInPackageJSON_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{invalid`), 0600)
	require.NoError(t, err)

	_, err = scriptExistsInPackageJSON(dir, "build")
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing package.json")
}

func TestDetectPackageManager_PnpmWorkspaceYaml(t *testing.T) {
	dir := t.TempDir()
	// Write package.json without packageManager field
	err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0600)
	require.NoError(t, err)
	// Write pnpm-workspace.yaml (signals pnpm even without pnpm-lock.yaml)
	err = os.WriteFile(filepath.Join(dir, "pnpm-workspace.yaml"), []byte("packages:\n  - 'apps/*'\n"), 0600)
	require.NoError(t, err)

	result := DetectPackageManager(dir)
	require.Equal(t, PackageManagerPnpm, result)
}

func TestIsDependenciesUpToDate_Npm(t *testing.T) {
	dir := t.TempDir()

	// No lock file → not up-to-date
	require.False(t, IsDependenciesUpToDate(dir, PackageManagerNpm))

	// Create lock file
	err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte("{}"), 0600)
	require.NoError(t, err)

	// No node_modules → not up-to-date
	require.False(t, IsDependenciesUpToDate(dir, PackageManagerNpm))

	// Create node_modules but no internal marker
	err = os.Mkdir(filepath.Join(dir, "node_modules"), 0700)
	require.NoError(t, err)
	require.False(t, IsDependenciesUpToDate(dir, PackageManagerNpm))

	// Create internal marker newer than lock file → up-to-date
	err = os.WriteFile(filepath.Join(dir, "node_modules", ".package-lock.json"), []byte("{}"), 0600)
	require.NoError(t, err)
	require.True(t, IsDependenciesUpToDate(dir, PackageManagerNpm))
}

func TestIsDependenciesUpToDate_Pnpm(t *testing.T) {
	dir := t.TempDir()

	// Create lock file
	err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0600)
	require.NoError(t, err)

	// No node_modules → not up-to-date
	require.False(t, IsDependenciesUpToDate(dir, PackageManagerPnpm))

	// Create node_modules but no .pnpm dir
	err = os.Mkdir(filepath.Join(dir, "node_modules"), 0700)
	require.NoError(t, err)
	require.False(t, IsDependenciesUpToDate(dir, PackageManagerPnpm))

	// Create .pnpm dir → up-to-date
	err = os.Mkdir(filepath.Join(dir, "node_modules", ".pnpm"), 0700)
	require.NoError(t, err)
	require.True(t, IsDependenciesUpToDate(dir, PackageManagerPnpm))
}

func TestIsDependenciesUpToDate_Yarn(t *testing.T) {
	dir := t.TempDir()

	// Create lock file
	err := os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0600)
	require.NoError(t, err)

	// No node_modules → not up-to-date
	require.False(t, IsDependenciesUpToDate(dir, PackageManagerYarn))

	// Create node_modules but no .yarn-integrity → not up-to-date
	err = os.Mkdir(filepath.Join(dir, "node_modules"), 0700)
	require.NoError(t, err)
	require.False(t, IsDependenciesUpToDate(dir, PackageManagerYarn))

	// Create .yarn-integrity → up-to-date
	err = os.WriteFile(filepath.Join(dir, "node_modules", ".yarn-integrity"), []byte("{}"), 0600)
	require.NoError(t, err)
	require.True(t, IsDependenciesUpToDate(dir, PackageManagerYarn))
}

func TestIsDependenciesUpToDate_YarnStaleLockFile(t *testing.T) {
	dir := t.TempDir()

	// Create node_modules and .yarn-integrity
	err := os.Mkdir(filepath.Join(dir, "node_modules"), 0700)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "node_modules", ".yarn-integrity"), []byte("{}"), 0600)
	require.NoError(t, err)

	// Create yarn.lock
	err = os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte("# updated"), 0600)
	require.NoError(t, err)

	// Explicitly set .yarn-integrity to be older than yarn.lock
	oldTime := time.Now().Add(-1 * time.Hour)
	err = os.Chtimes(filepath.Join(dir, "node_modules", ".yarn-integrity"), oldTime, oldTime)
	require.NoError(t, err)

	// .yarn-integrity is older than yarn.lock → stale → not up-to-date
	require.False(t, IsDependenciesUpToDate(dir, PackageManagerYarn))
}
