// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"testing"

	"github.com/stretchr/testify/require"

	"azureaiagent/internal/pkg/agents/agent_api"
)

// testHarness is the harness block the gate cases below attach. Only the type
// matters to these gates, so every case shares one value.
var testHarness = NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot)

// TestValidateHarnessBlock covers the final type-only harness contract.
func TestValidateHarnessBlock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		agent       PromptAgent
		wantErr     bool
		wantMessage string
	}{
		{
			name:  "no harness block at all",
			agent: PromptAgent{},
		},
		{
			name:  "type alone is a complete block",
			agent: PromptAgent{Harness: testHarness},
		},
		{
			name:        "a block with no type is rejected",
			agent:       PromptAgent{Harness: &PromptHarness{}},
			wantErr:     true,
			wantMessage: "harness with no type",
		},
		{
			name: "future harness type is accepted",
			agent: PromptAgent{Harness: &PromptHarness{
				Type: "some_future_harness",
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.agent.ValidateHarnessBlock()
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMessage)
		})
	}
}
