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

// TestValidateHarnessBlock covers the shape of the harness block itself: the
// rules the service enforces on `type`, `environment`, and `builtin_tools`.
func TestValidateHarnessBlock(t *testing.T) {
	t.Parallel()

	idle := 900

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
			name: "cpu and memory together are accepted",
			agent: PromptAgent{Harness: &PromptHarness{
				Type:        agent_api.ManagedAgentHarnessGitHubCopilot,
				Environment: &PromptHarnessEnvironment{Cpu: "1", Memory: "2Gi", IdleTimeoutSeconds: &idle},
			}},
		},
		{
			name: "idle timeout alone is accepted",
			agent: PromptAgent{Harness: &PromptHarness{
				Type:        agent_api.ManagedAgentHarnessGitHubCopilot,
				Environment: &PromptHarnessEnvironment{IdleTimeoutSeconds: &idle},
			}},
		},
		{
			name: "cpu without memory is rejected",
			agent: PromptAgent{Harness: &PromptHarness{
				Type:        agent_api.ManagedAgentHarnessGitHubCopilot,
				Environment: &PromptHarnessEnvironment{Cpu: "1"},
			}},
			wantErr:     true,
			wantMessage: "harness.environment.cpu without harness.environment.memory",
		},
		{
			name: "memory without cpu is rejected",
			agent: PromptAgent{Harness: &PromptHarness{
				Type:        agent_api.ManagedAgentHarnessGitHubCopilot,
				Environment: &PromptHarnessEnvironment{Memory: "2Gi"},
			}},
			wantErr:     true,
			wantMessage: "harness.environment.memory without harness.environment.cpu",
		},
		{
			name: "known capabilities are accepted",
			agent: PromptAgent{Harness: &PromptHarness{
				Type: agent_api.ManagedAgentHarnessGitHubCopilot,
				BuiltinTools: &PromptHarnessBuiltInTools{
					Allowed:  &[]string{"filesystem_read", "web", "subagents"},
					Excluded: &[]string{"shell"},
				},
			}},
		},
		{
			name: "an empty allowed list turns everything off and is accepted",
			agent: PromptAgent{Harness: &PromptHarness{
				Type:         agent_api.ManagedAgentHarnessGitHubCopilot,
				BuiltinTools: &PromptHarnessBuiltInTools{Allowed: &[]string{}},
			}},
		},
		{
			name: "an unknown capability is rejected rather than dropped",
			agent: PromptAgent{Harness: &PromptHarness{
				Type: agent_api.ManagedAgentHarnessGitHubCopilot,
				BuiltinTools: &PromptHarnessBuiltInTools{
					Allowed:  &[]string{"filesystem_reed"},
					Excluded: &[]string{"netwrok"},
				},
			}},
			wantErr:     true,
			wantMessage: "harness.builtin_tools.allowed.filesystem_reed",
		},
		{
			name: "future harness is not given GitHub Copilot block restrictions",
			agent: PromptAgent{Harness: &PromptHarness{
				Type:        "some_future_harness",
				Environment: &PromptHarnessEnvironment{Cpu: "future-size"},
				BuiltinTools: &PromptHarnessBuiltInTools{
					Allowed: &[]string{"future_capability"},
				},
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
