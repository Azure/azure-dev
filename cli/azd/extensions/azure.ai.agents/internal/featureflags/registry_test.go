// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package featureflags

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistryDeclarations(t *testing.T) {
	features := All()
	require.Equal(t, []Feature{ManagedHarness, Voice, LongRunning, A2A}, features)
	require.Equal(t,
		[]string{"agents.managed_harness", "agents.voice", "agents.long_running", "agents.a2a"},
		[]string{ManagedHarness.ID, Voice.ID, LongRunning.ID, A2A.ID},
	)

	ids := map[string]bool{}
	envVars := map[string]bool{}
	for _, feature := range features {
		require.True(t, strings.HasPrefix(feature.ID, "agents."), feature.ID)
		require.Equal(t,
			"AZD_ALPHA_ENABLE_"+strings.ToUpper(strings.ReplaceAll(feature.ID, ".", "_")),
			feature.EnvVar,
		)
		require.NotEmpty(t, feature.Description)
		require.False(t, ids[feature.ID], "duplicate ID %s", feature.ID)
		require.False(t, envVars[feature.EnvVar], "duplicate env var %s", feature.EnvVar)
		ids[feature.ID] = true
		envVars[feature.EnvVar] = true
	}
}

func TestAllReturnsCopy(t *testing.T) {
	features := All()
	features[0].ID = "changed"
	require.Equal(t, ManagedHarness, All()[0])
	require.Equal(t, "agents.managed_harness", ManagedHarness.ID)
}

func TestLookup(t *testing.T) {
	for _, feature := range All() {
		found, ok := Lookup(feature.ID)
		require.True(t, ok)
		require.Equal(t, feature, found)
	}
	_, ok := Lookup("agents.hosted")
	require.False(t, ok)
	_, ok = Lookup("")
	require.False(t, ok)
}
