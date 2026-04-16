// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeLoginServer(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain login server", "crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"with https prefix", "https://crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"with http prefix", "http://crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"with trailing slash", "crfoo.azurecr.io/", "crfoo.azurecr.io"},
		{"uppercase domain", "CrFoo.AzureCR.io", "crfoo.azurecr.io"},
		{"uppercase HTTPS prefix", "HTTPS://crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"mixed case prefix and domain", "Https://CrFoo.AzureCR.io/", "crfoo.azurecr.io"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeLoginServer(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDeveloperRBACRoleConstants(t *testing.T) {
	// ACR roles
	assert.Equal(t, "fb382eab-e894-4461-af04-94435c366c3f", roleContainerRegistryTasksContributor)
	assert.Equal(t, "2efddaa5-3f1f-4df3-97df-af3f13818f4c", roleContainerRegistryRepositoryContributor)
	assert.Equal(t, "8311e382-0749-4cb8-b61a-304f252e45ec", roleAcrPush)
	assert.Equal(t, "2a1e307c-b015-4ebd-883e-5b7698a07328", roleAcrRepositoryWriter)

	// Superset roles
	assert.Equal(t, "8e3af657-a8ff-443c-a75c-2fe8c4bcb635", roleOwner)
	assert.Equal(t, "b24988ac-6180-42a0-ab88-20f7382dd24c", roleContributor)

	// Role-assignment write roles
	assert.Equal(t, "18d7d88d-d35e-4fb5-a5c3-7773c20a72d9", roleUserAccessAdministrator)
	assert.Equal(t, "f58310d9-a9f6-439a-9e8d-f62e7b41a168", roleRBACAdministrator)

	// AI roles
	assert.Equal(t, "64702f94-c441-49e6-a78b-ef80e0188fee", roleAzureAIDeveloper)
}

func TestSufficientRoleLists(t *testing.T) {
	// Verify the sufficient role lists contain expected entries.
	assert.Contains(t, sufficientACRRoles, roleOwner)
	assert.Contains(t, sufficientACRRoles, roleContributor)
	assert.Contains(t, sufficientACRRoles, roleAcrPush)
	assert.Contains(t, sufficientACRRoles, roleContainerRegistryTasksContributor)
	assert.Contains(t, sufficientACRRoles, roleContainerRegistryRepositoryContributor)

	assert.Contains(t, sufficientAIUserRoles, roleOwner)
	assert.Contains(t, sufficientAIUserRoles, roleContributor)
	assert.Contains(t, sufficientAIUserRoles, roleAzureAIUser)
	assert.Contains(t, sufficientAIUserRoles, roleAzureAIDeveloper)

	// Role-assignment write: Owner, UAA, RBAC Admin; Contributor must NOT be included.
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleOwner)
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleUserAccessAdministrator)
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleRBACAdministrator)
	assert.NotContains(t, sufficientRoleAssignWriteRoles, roleContributor)

	// ABAC ACR roles: Owner, RepositoryWriter, RepositoryContributor; AcrPush must NOT be included.
	assert.Contains(t, sufficientACRAbacRoles, roleOwner)
	assert.Contains(t, sufficientACRAbacRoles, roleAcrRepositoryWriter)
	assert.Contains(t, sufficientACRAbacRoles, roleContainerRegistryRepositoryContributor)
	assert.NotContains(t, sufficientACRAbacRoles, roleAcrPush)
}
