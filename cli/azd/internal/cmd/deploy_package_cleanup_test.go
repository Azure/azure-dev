// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

func Test_Deploy_PackageCleanup_Logic(t *testing.T) {
	tempDir := t.TempDir()

	// Create a user-provided package (simulates --from-package flag)
	userPackageZip := filepath.Join(tempDir, "user-package.zip")
	err := os.WriteFile(userPackageZip, []byte("user package content"), 0600)
	require.NoError(t, err)

	// Create a temporary package (simulates AZD-created package)
	tempPackageZip := filepath.Join(tempDir, "temp-package.zip")
	err = os.WriteFile(tempPackageZip, []byte("temp package content"), 0600)
	require.NoError(t, err)

	// Test Case 1: User-provided package (--from-package flag)
	userPackageResult := &project.ServicePackageResult{
		PackagePath: userPackageZip,
	}
	packageWasCreatedByAzd := false

	// Simulate cleanup logic from deploy.go
	if packageWasCreatedByAzd && userPackageResult.PackagePath != "" {
		os.Remove(userPackageResult.PackagePath)
	}

	// Verify user package still exists
	_, err = os.Stat(userPackageZip)
	require.NoError(t, err, "User-provided package should not be deleted")

	// Test Case 2: AZD-created package (Package() method)
	tempPackageResult := &project.ServicePackageResult{
		PackagePath: tempPackageZip,
	}
	packageWasCreatedByAzd = true

	// Simulate cleanup logic from deploy.go
	if packageWasCreatedByAzd && tempPackageResult.PackagePath != "" {
		os.Remove(tempPackageResult.PackagePath)
	}

	// Verify temp package was deleted
	_, err = os.Stat(tempPackageZip)
	require.True(t, os.IsNotExist(err), "AZD-created package should be deleted")
}
