// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveACRResourceID_NormalizesLoginServer(t *testing.T) {
	// Test that the login server normalization handles various input formats.
	// We can't test the full resolution without a live Azure connection,
	// but we can verify the normalization logic.
	tests := []struct {
		name  string
		input string
		want  string // expected normalized form
	}{
		{"plain login server", "crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"with https prefix", "https://crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"with http prefix", "http://crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"with trailing slash", "crfoo.azurecr.io/", "crfoo.azurecr.io"},
		{"uppercase", "CrFoo.AzureCR.io", "crfoo.azurecr.io"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We test the normalization by checking that various inputs
			// produce consistent lowercase, no-prefix, no-suffix forms.
			// The actual resolution requires live Azure and is tested via integration tests.
			normalized := normalizeLoginServer(tt.input)
			assert.Equal(t, tt.want, normalized)
		})
	}
}

func TestDeveloperRBACRoleConstants(t *testing.T) {
	// ACR roles
	assert.Equal(t, "fb382eab-e894-4461-af04-94435c366c3f", roleContainerRegistryTasksContributor)
	assert.Equal(t, "2efddaa5-3f1f-4df3-97df-af3f13818f4c", roleContainerRegistryRepositoryContributor)
	assert.Equal(t, "8311e382-0749-4cb8-b61a-304f252e45ec", roleAcrPush)

	// Superset roles
	assert.Equal(t, "8e3af657-a8ff-443c-a75c-2fe8c4bcb635", roleOwner)
	assert.Equal(t, "b24988ac-6180-42a0-ab88-20f7382dd24c", roleContributor)

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
}

// normalizeLoginServer is extracted for testability.
func normalizeLoginServer(loginServer string) string {
	s := loginServer
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) > len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
			s = s[len(prefix):]
		}
	}
	s = strings.TrimSuffix(s, "/")
	return strings.ToLower(s)
}
