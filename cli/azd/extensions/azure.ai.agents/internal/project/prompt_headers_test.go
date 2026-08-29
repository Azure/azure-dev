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

	plain := promptAgentRequestHeaders(&agent_yaml.PromptAgent{}, settings)
	require.Equal(t, "https://model.example.com", plain["x-model-endpoint"])
	require.NotContains(t, plain, "Foundry-Features")

	harnessed := promptAgentRequestHeaders(&agent_yaml.PromptAgent{
		Harness: agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot),
	}, settings)
	require.Equal(t, "GitHubCopilot=V1Preview", harnessed["Foundry-Features"])
}
