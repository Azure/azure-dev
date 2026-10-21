// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package featureflags

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestList(t *testing.T) {
	features := List()
	require.Equal(t, []Feature{LongRunning, ManagedHarness, Voice}, features)
	require.Equal(t, []string{
		"agents.long_running", "agents.managed_harness", "agents.voice",
	}, []string{features[0].ID, features[1].ID, features[2].ID})
	require.Equal(t, []string{
		"AZD_FOUNDRY_EXT_ENABLE_AGENTS_LONG_RUNNING",
		"AZD_FOUNDRY_EXT_ENABLE_AGENTS_MANAGED_HARNESS",
		"AZD_FOUNDRY_EXT_ENABLE_AGENTS_VOICE",
	}, []string{features[0].EnvVar, features[1].EnvVar, features[2].EnvVar})
	for _, feature := range features {
		require.NotEmpty(t, feature.Description)
	}

	features[0] = Feature{ID: "changed", Description: "changed", EnvVar: "CHANGED"}
	require.Equal(t, []Feature{LongRunning, ManagedHarness, Voice}, List())
}

func TestAgentPreviewFourSourceCombinations(t *testing.T) {
	for _, feature := range []Feature{ManagedHarness, Voice, LongRunning} {
		for mask := range 16 {
			t.Run(fmt.Sprintf("%s/%04b", feature.ID, mask), func(t *testing.T) {
				t.Setenv(feature.EnvVar, fmt.Sprint(mask&1 != 0))
				value := "off"
				if mask&4 != 0 {
					value = "on"
				}
				alpha, err := json.Marshal(map[string]any{
					"agents": map[string]any{strings.TrimPrefix(feature.ID, "agents."): value},
				})
				require.NoError(t, err)
				client := newMockClient(t)
				client.expectSources(
					&azdext.GetUserConfigResponse{Found: true, Value: alpha},
					projectWithServices(&azdext.ServiceConfig{Host: "containerapp",
						Environment: map[string]string{feature.EnvVar: fmt.Sprint(mask&8 != 0)}}),
					map[string]string{feature.EnvVar: fmt.Sprint(mask&2 != 0)}, "selected",
				)
				enabled, err := isEnabled(t.Context(), client, feature, "selected")
				require.NoError(t, err)
				require.Equal(t, mask != 0, enabled)
			})
		}
	}
}

func TestPreviewEnvironmentSelection(t *testing.T) {
	t.Setenv("AZD_ENVIRONMENT", "host-selected")
	require.Equal(t, "host-selected", Environment(t.Context()))
	require.Equal(t, "explicit", Environment(WithEnvironment(t.Context(), "explicit")))
	require.Empty(t, Environment(WithEnvironment(t.Context(), "")))
}
