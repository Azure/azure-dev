// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestHostedVoiceStandaloneInitRejected(t *testing.T) {
	t.Parallel()
	for _, kind := range []agent_yaml.AgentKind{agent_yaml.AgentKindVoice, agent_yaml.AgentKindPromptVoice} {
		for _, shape := range []string{"conversation-engine", "legacy-target"} {
			t.Run(string(kind)+"/"+shape, func(t *testing.T) {
				t.Parallel()
				voice := agent_yaml.VoiceAgent{
					AgentDefinition: agent_yaml.AgentDefinition{Kind: kind, Name: "wrapper"},
				}
				if shape == "conversation-engine" {
					voice.ConversationEngine = &agent_yaml.VoiceConversationEngine{
						Type: "hosted_agent", Name: "target",
					}
				} else {
					voice.ModelType = agent_yaml.VoiceModelTypeHostedAgent
					voice.TargetAgent = &agent_yaml.VoiceTargetAgent{Service: "target"}
				}
				// No azd client: rejection must happen before mutating project state.
				action := &InitAction{}
				err := action.addVoiceAgentToProject(t.Context(), t.TempDir(), &agent_yaml.AgentManifest{Template: voice})
				require.ErrorContains(t, err, "hosted voice wrappers cannot be initialized from a standalone voice manifest")
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, "use a sample azure.yaml that declares both the hosted target and the voice wrapper", local.Suggestion)
			})
		}
	}
}
