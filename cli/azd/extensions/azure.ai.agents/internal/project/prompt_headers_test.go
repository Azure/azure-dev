// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/stretchr/testify/require"
)

func TestPromptAgentRequestHeaders(t *testing.T) {
	settings := &PromptAgentSettings{ModelEndpoint: "https://model.example.com"}
	for _, tt := range []struct {
		name     string
		agent    agent_yaml.PromptAgent
		features string
	}{
		{name: "plain"},
		{name: "managed", agent: agent_yaml.PromptAgent{
			Harness: agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot),
		}, features: "GitHubCopilot=V1Preview"},
		{name: "plain skills", agent: agent_yaml.PromptAgent{
			ResolvedSkills: []agent_yaml.HarnessSkillRef{{Name: "skill", Version: "1"}},
		}, features: "Skills=V1Preview"},
		{name: "managed skills", agent: agent_yaml.PromptAgent{
			Harness:        agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot),
			ResolvedSkills: []agent_yaml.HarnessSkillRef{{Name: "skill", Version: "1"}},
		}, features: "GitHubCopilot=V1Preview,Skills=V1Preview"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			headers := promptAgentRequestHeaders(&tt.agent, settings)
			require.Equal(t, "https://model.example.com", headers["x-model-endpoint"])
			if tt.features == "" {
				require.NotContains(t, headers, "Foundry-Features")
			} else {
				require.Equal(t, tt.features, headers["Foundry-Features"])
			}
		})
	}
}
