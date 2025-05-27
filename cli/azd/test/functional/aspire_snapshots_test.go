// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSnapshotsForAspire tests that the snapshots for Aspire resources do not contain the explicitContributorUserRoleAssignment
func TestSnapshotsForAspire(t *testing.T) {
	// Check all resources.bicep snapshots to ensure they don't have explicitContributorUserRoleAssignment
	snapshotFiles := []string{
		"testdata/snaps/aspire-full/infra/resources.bicep",
	}

	for _, file := range snapshotFiles {
		content, err := os.ReadFile(file)
		require.NoError(t, err)

		// Check that the explicitContributorUserRoleAssignment is not in the file
		require.False(t, 
			strings.Contains(string(content), "explicitContributorUserRoleAssignment"), 
			"File %s still contains explicitContributorUserRoleAssignment", file)
	}
}