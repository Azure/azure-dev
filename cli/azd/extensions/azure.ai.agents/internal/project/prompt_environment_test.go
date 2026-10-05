// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestRegisterPromptAgentEnvVarsWritesOwnershipBeforeReady(t *testing.T) {
	tests := []struct {
		name       string
		harnessed  bool
		wantTarget string
	}{
		{
			name:       "plain prompt agent",
			wantTarget: "https://acct.services.ai.azure.com/api/projects/project/openai/v1/responses",
		},
		{
			name:      "harnessed prompt agent",
			harnessed: true,
			wantTarget: "https://acct.services.ai.azure.com/api/projects/project/agents/agent/" +
				"endpoint/protocols/openai/responses?api-version=v1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envServer := &stubEnvServer{}
			provider := &AgentServiceTargetProvider{
				azdClient: newEnvTestClient(t, envServer),
				env:       &azdext.Environment{Name: "dev"},
			}
			service := &azdext.ServiceConfig{Name: "prompt-agent"}
			settings := &PromptAgentSettings{
				ProjectEndpoint: "https://acct.services.ai.azure.com/api/projects/project/",
			}

			err := provider.registerPromptAgentEnvVars(
				t.Context(), service, "agent", "3", settings, test.harnessed,
				map[string]any{memoryStoreBindingKey: "memory"})
			require.NoError(t, err)

			require.GreaterOrEqual(t, len(envServer.writes), 6)
			requests := envServer.writes
			require.Equal(t, "AGENT_PROMPT_AGENT_VERSION", requests[0].Key)
			require.Empty(t, requests[0].Value)
			require.Equal(t, "AGENT_PROMPT_AGENT_ENDPOINT", requests[2].Key)
			require.Equal(t, test.wantTarget, requests[2].Value)
			require.Equal(t, envkey.AgentProjectEndpoint(service.Name), requests[len(requests)-2].Key)
			require.Equal(
				t,
				"https://acct.services.ai.azure.com/api/projects/project",
				requests[len(requests)-2].Value,
			)
			require.Equal(t, "AGENT_PROMPT_AGENT_VERSION", requests[len(requests)-1].Key)
			require.Equal(t, "3", requests[len(requests)-1].Value)
		})
	}
}
