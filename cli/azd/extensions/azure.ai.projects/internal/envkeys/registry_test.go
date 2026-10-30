// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package envkeys

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistry(t *testing.T) {
	var names []string
	seen := map[string]bool{}
	for _, key := range []interface{ Name() string }{
		ContainerRegistryResourceID, ContainerRegistryEndpoint,
		FoundryAcrMode, ProjectAcrConnectionName,
		FoundryAcrPullAssigned, AgentSkipAcr,
	} {
		require.False(t, seen[key.Name()], "duplicate key %s", key.Name())
		seen[key.Name()] = true
		names = append(names, key.Name())
		t.Log(key.Name())
	}
	require.Equal(t, []string{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
		"AZURE_CONTAINER_REGISTRY_ENDPOINT",
		"AZD_FOUNDRY_ACR_MODE",
		"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
		"AZD_FOUNDRY_ACR_PULL_ASSIGNED",
		"AZD_AGENT_SKIP_ACR",
	}, names)

	doc, err := os.ReadFile("doc.go")
	require.NoError(t, err)
	for _, name := range names {
		require.Equal(t, 1, strings.Count(string(doc), name), "document %s exactly once", name)
	}
}
