// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/require"
)

func TestValidateInitKindHarness(t *testing.T) {
	tests := []struct {
		name    string
		kind    agentKindChoice
		rawKind string
		harness string
		wantErr string
	}{
		{name: "prompt harness", kind: AgentKindChoicePrompt, rawKind: "prompt",
			harness: agent_api.ManagedAgentHarnessGitHubCopilot},
		{name: "hosted rejects harness", kind: AgentKindChoiceHosted, rawKind: "hosted",
			harness: agent_api.ManagedAgentHarnessGitHubCopilot, wantErr: "only valid with --kind prompt"},
		{name: "missing kind rejects harness", harness: agent_api.ManagedAgentHarnessGitHubCopilot,
			wantErr: "only valid with --kind prompt"},
		{name: "managed is not a kind", kind: agentKindChoice("managed"), rawKind: "managed",
			wantErr: "unknown --kind"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateInitKindHarness(test.kind, test.rawKind, test.harness, false)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestValidateInitInstructions(t *testing.T) {
	require.NoError(t, validateInitInstructions(AgentKindChoicePrompt, "prompt", "Be concise.", false))
	require.ErrorContains(t,
		validateInitInstructions(AgentKindChoiceHosted, "hosted", "Be concise.", false),
		"only supported for prompt agents")
}
