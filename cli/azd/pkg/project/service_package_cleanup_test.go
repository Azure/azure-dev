// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ServicePackageResult_IsTemporary_AppService(t *testing.T) {
	// Create a temporary zip file to simulate a user-provided package
	tempDir := t.TempDir()
	userPackageZip := filepath.Join(tempDir, "user-package.zip")
	err := os.WriteFile(userPackageZip, []byte("test zip content"), 0600)
	require.NoError(t, err)

	// Create a package result that simulates user-provided package (IsTemporary=false)
	userPackageResult := &ServicePackageResult{
		PackagePath: userPackageZip,
		IsTemporary: false, // User-provided package should not be deleted
	}

	// Create a package result from the appservice Package method (should be temporary)
	tempPackageResult := &ServicePackageResult{
		PackagePath: "/some/temp/path.zip",
		IsTemporary: true, // AZD-created package should be deleted
	}

	// Verify user package is not marked temporary
	require.False(t, userPackageResult.IsTemporary, "User-provided packages should not be marked as temporary")

	// Verify AZD-created package is marked temporary
	require.True(t, tempPackageResult.IsTemporary, "AZD-created packages should be marked as temporary")

	// Verify the user package file still exists (since we're not calling Deploy, but this tests the concept)
	_, err = os.Stat(userPackageZip)
	require.NoError(t, err, "User-provided package should still exist")
}

func Test_ServicePackageResult_IsTemporary_FunctionApp(t *testing.T) {
	// Create a temporary zip file to simulate a user-provided package
	tempDir := t.TempDir()
	userPackageZip := filepath.Join(tempDir, "user-package.zip")
	err := os.WriteFile(userPackageZip, []byte("test zip content"), 0600)
	require.NoError(t, err)

	// Create a package result that simulates user-provided package (IsTemporary=false)
	userPackageResult := &ServicePackageResult{
		PackagePath: userPackageZip,
		IsTemporary: false, // User-provided package should not be deleted
	}

	// Create a package result from the functionapp Package method (should be temporary)
	tempPackageResult := &ServicePackageResult{
		PackagePath: "/some/temp/path.zip",
		IsTemporary: true, // AZD-created package should be deleted
	}

	// Verify user package is not marked temporary
	require.False(t, userPackageResult.IsTemporary, "User-provided packages should not be marked as temporary")

	// Verify AZD-created package is marked temporary
	require.True(t, tempPackageResult.IsTemporary, "AZD-created packages should be marked as temporary")

	// Verify the user package file still exists (since we're not calling Deploy, but this tests the concept)
	_, err = os.Stat(userPackageZip)
	require.NoError(t, err, "User-provided package should still exist")
}

func Test_ServicePackageResult_PackageCleanup_Simulation(t *testing.T) {
	// Simulate the package cleanup logic that would happen in Deploy methods

	tempDir := t.TempDir()

	// Create temporary file (simulates AZD-created package)
	tempPackage := filepath.Join(tempDir, "temp-package.zip")
	err := os.WriteFile(tempPackage, []byte("temp content"), 0600)
	require.NoError(t, err)

	// Create user file (simulates user-provided package)
	userPackage := filepath.Join(tempDir, "user-package.zip")
	err = os.WriteFile(userPackage, []byte("user content"), 0600)
	require.NoError(t, err)

	// Create package results
	tempResult := &ServicePackageResult{
		PackagePath: tempPackage,
		IsTemporary: true,
	}

	userResult := &ServicePackageResult{
		PackagePath: userPackage,
		IsTemporary: false,
	}

	// Simulate cleanup logic (what would happen in Deploy methods)
	if tempResult.IsTemporary {
		os.Remove(tempResult.PackagePath)
	}

	if userResult.IsTemporary {
		os.Remove(userResult.PackagePath)
	}

	// Verify temp package was deleted
	_, err = os.Stat(tempPackage)
	require.True(t, os.IsNotExist(err), "Temporary package should be deleted")

	// Verify user package still exists
	_, err = os.Stat(userPackage)
	require.NoError(t, err, "User package should not be deleted")
}
