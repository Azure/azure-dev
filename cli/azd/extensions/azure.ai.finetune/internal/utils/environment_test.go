// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvironmentConstants_AreUniqueAndNonEmpty(t *testing.T) {
	// This test prevents bugs where:
	// 1. A constant is accidentally left empty
	// 2. Two constants have the same value (copy-paste error)
	constants := []string{
		EnvAzureTenantID,
		EnvAzureSubscriptionID,
		EnvAzureLocation,
		EnvAzureAccountName,
		EnvAzureOpenAIProjectName,
		EnvAPIVersion,
		EnvFinetuningRoute,
		EnvFinetuningTokenScope,
	}

	seen := make(map[string]bool)
	for _, c := range constants {
		require.NotEmpty(t, c, "Environment constant should not be empty")
		require.False(t, seen[c], "Duplicate environment constant value: %s", c)
		seen[c] = true
	}
}

func TestEnvironmentConstants_FollowAzureNamingConvention(t *testing.T) {
	// Ensures constants follow AZURE_ prefix convention for Azure-related env vars
	// This prevents misconfiguration when users set environment variables
	azureConstants := []string{
		EnvAzureTenantID,
		EnvAzureSubscriptionID,
		EnvAzureLocation,
		EnvAzureAccountName,
		EnvAzureOpenAIProjectName,
		EnvAPIVersion,
		EnvFinetuningRoute,
		EnvFinetuningTokenScope,
	}

	for _, c := range azureConstants {
		require.Contains(t, c, "AZURE_", "Azure environment constant %q should contain AZURE_ prefix", c)
	}
}
