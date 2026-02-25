// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRoleAssignmentPermissionError(t *testing.T) {
	err := &RoleAssignmentPermissionError{
		SubscriptionId: "sub-123",
		PrincipalId:    "principal-456",
	}

	msg := err.Error()
	require.Contains(t, msg, "principal-456")
	require.Contains(t, msg, "sub-123")
	require.Contains(t, msg, "Microsoft.Authorization/roleAssignments/write")
	require.Contains(t, msg, "User Access Administrator")
	require.Contains(t, msg, "Owner")
}
