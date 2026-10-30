// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeploymentSafetyCheckerAllowsOnlyAuthoritativeAbsence(t *testing.T) {
	listCalls := 0
	checker := &azureDeploymentSafetyChecker{
		verifyOwnership: func(context.Context, string, map[string]string, string) error {
			return errPostgreSQLResourceNotFound
		},
		verifyNoOwnedResources: func(context.Context, string, map[string]string) error {
			listCalls++
			return nil
		},
	}

	err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{
		azureSubscriptionIDEnv: "sub",
		postgresServerIDEnv: "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.DBforPostgreSQL/flexibleServers/pg",
	})
	require.NoError(t, err)
	require.Equal(t, 1, listCalls)
}

func TestDeploymentSafetyCheckerBlocksExistingOwnedResource(t *testing.T) {
	listCalls := 0
	checker := &azureDeploymentSafetyChecker{
		verifyOwnership: func(context.Context, string, map[string]string, string) error {
			return nil
		},
		verifyNoOwnedResources: func(context.Context, string, map[string]string) error {
			listCalls++
			return nil
		},
	}

	err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{
		azureSubscriptionIDEnv: "sub",
		postgresServerIDEnv: "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.DBforPostgreSQL/flexibleServers/pg",
	})
	require.ErrorContains(t, err, "existing infrastructure")
	require.Zero(t, listCalls)
}

func TestDeploymentSafetyCheckerFailsClosedOnReadErrors(t *testing.T) {
	checker := &azureDeploymentSafetyChecker{
		verifyOwnership: func(context.Context, string, map[string]string, string) error {
			return errors.New("authorization denied")
		},
		verifyNoOwnedResources: func(context.Context, string, map[string]string) error {
			return nil
		},
	}

	err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{
		azureSubscriptionIDEnv: "sub",
		postgresServerIDEnv: "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.DBforPostgreSQL/flexibleServers/pg",
	})
	require.ErrorContains(t, err, "authorization denied")
}

func TestDeploymentSafetyCheckerConstructsPersistedResourceIdentity(t *testing.T) {
	var resourceID string
	checker := &azureDeploymentSafetyChecker{
		verifyOwnership: func(
			_ context.Context,
			_ string,
			_ map[string]string,
			id string,
		) error {
			resourceID = id
			return errPostgreSQLResourceNotFound
		},
		verifyNoOwnedResources: func(context.Context, string, map[string]string) error {
			return nil
		},
	}

	err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{
		azureSubscriptionIDEnv: "sub",
		azureResourceGroupEnv:  "custom-rg",
		postgresServerNameEnv:  "custom-postgres",
	})
	require.NoError(t, err)
	require.Equal(
		t,
		"/subscriptions/sub/resourceGroups/custom-rg/providers/"+
			"Microsoft.DBforPostgreSQL/flexibleServers/custom-postgres",
		resourceID,
	)
}

func TestDeploymentSafetyCheckerRequiresSubscriptionBeforeReads(t *testing.T) {
	called := false
	checker := &azureDeploymentSafetyChecker{
		verifyOwnership: func(context.Context, string, map[string]string, string) error {
			called = true
			return nil
		},
		verifyNoOwnedResources: func(context.Context, string, map[string]string) error {
			called = true
			return nil
		},
	}

	err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{})
	require.ErrorContains(t, err, "subscription")
	require.False(t, called)
}

func TestDeploymentSafetyCheckerBlocksOwnedResourceDiscoveredByList(t *testing.T) {
	checker := &azureDeploymentSafetyChecker{
		verifyNoOwnedResources: func(context.Context, string, map[string]string) error {
			return missingExistingDatabasePasswordError()
		},
	}

	err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{
		azureSubscriptionIDEnv: "sub",
	})
	require.ErrorContains(t, err, "existing infrastructure")
}

func TestParsePostgreSQLResourceIDRejectsAnotherResourceType(t *testing.T) {
	_, err := parsePostgreSQLResourceID(
		"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/pg",
	)
	require.ErrorContains(t, err, "invalid PostgreSQL resource ID")
}
