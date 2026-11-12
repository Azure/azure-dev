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
		{name: "Microsoft Copilot", agent: agent_yaml.PromptAgent{
			Harness: agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessMicrosoftCopilot),
		}, features: "MicrosoftCopilot=V1Preview"},
		{name: "legacy GitHub Copilot", agent: agent_yaml.PromptAgent{
			Harness: agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot),
		}, features: "MicrosoftCopilot=V1Preview"},
		{name: "other harness", agent: agent_yaml.PromptAgent{
			Harness: agent_yaml.NewPromptHarness("other"),
		}},
		{name: "Copilot prefix is not a match", agent: agent_yaml.PromptAgent{
			Harness: agent_yaml.NewPromptHarness("microsoft_copilot_preview_other"),
		}},
		{name: "plain skills", agent: agent_yaml.PromptAgent{
			ResolvedSkills: []agent_yaml.HarnessSkillRef{{Name: "skill", Version: "1"}},
		}, features: "Skills=V1Preview"},
		{name: "Microsoft Copilot skills", agent: agent_yaml.PromptAgent{
			Harness:        agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessMicrosoftCopilot),
			ResolvedSkills: []agent_yaml.HarnessSkillRef{{Name: "skill", Version: "1"}},
		}, features: "MicrosoftCopilot=V1Preview,Skills=V1Preview"},
		{name: "legacy GitHub Copilot skills", agent: agent_yaml.PromptAgent{
			Harness:        agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot),
			ResolvedSkills: []agent_yaml.HarnessSkillRef{{Name: "skill", Version: "1"}},
		}, features: "MicrosoftCopilot=V1Preview,Skills=V1Preview"},
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
