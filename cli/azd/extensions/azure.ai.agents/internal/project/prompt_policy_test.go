// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestExpandPromptAgentPolicies(t *testing.T) {
	t.Parallel()
	managed := agent_yaml.PromptAgent{Policies: []agent_yaml.Policy{{
		Type: agent_yaml.PolicyTypeRai, RaiPolicyName: "${RAI_POLICY_ID}",
	}}}
	require.NoError(t, expandPromptAgentPolicies(&managed, map[string]string{"RAI_POLICY_ID": raiPolicyID}))
	require.Equal(t, raiPolicyID, managed.Policies[0].RaiPolicyName)
	require.NoError(t, managed.ValidatePolicies())
}

func TestExpandPromptAgentPoliciesUnresolved(t *testing.T) {
	t.Parallel()
	managed := agent_yaml.PromptAgent{Policies: []agent_yaml.Policy{{
		Type: agent_yaml.PolicyTypeRai, RaiPolicyName: "${RAI_POLICY_ID}",
	}}}
	err := expandPromptAgentPolicies(&managed, map[string]string{})
	require.ErrorContains(t, err, "not set in the azd environment")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Suggestion, "azd provision")
}

func TestExpandPromptAgentPoliciesLeavesLiterals(t *testing.T) {
	t.Parallel()
	managed := agent_yaml.PromptAgent{Policies: []agent_yaml.Policy{{
		Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID,
	}}}
	require.NoError(t, expandPromptAgentPolicies(&managed, nil))
	require.Equal(t, raiPolicyID, managed.Policies[0].RaiPolicyName)
}

func TestPromptAgentPoliciesReachRaiConfig(t *testing.T) {
	t.Parallel()
	for _, harness := range []*agent_yaml.PromptHarness{
		nil,
		{Type: agent_api.ManagedAgentHarnessGitHubCopilot},
	} {
		managed := agent_yaml.PromptAgent{
			AgentDefinition: agent_yaml.AgentDefinition{Name: "rai-agent", Kind: agent_yaml.AgentKindPrompt},
			Model:           "gpt-4.1-mini", Instructions: "be helpful", Harness: harness,
			Policies: []agent_yaml.Policy{{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID}},
		}
		request, err := agent_yaml.CreatePromptAgentAPIRequest(managed, nil)
		require.NoError(t, err)
		definition := request.Definition.(agent_api.ManagedAgentDefinition)
		require.Equal(t, raiPolicyID, definition.RaiConfig.RaiPolicyName)
	}
}

func TestPromptCreateErrorAddsPolicySuggestion(t *testing.T) {
	t.Parallel()
	managed := agent_yaml.PromptAgent{
		Harness:  &agent_yaml.PromptHarness{Type: agent_api.ManagedAgentHarnessGitHubCopilot},
		Policies: []agent_yaml.Policy{{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID}},
	}
	err := promptCreateError(errors.New("BadRequest"), &managed)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Suggestion, "Responsible AI policy")
}

func TestPromptCreateErrorWithoutPolicyIsUnchanged(t *testing.T) {
	t.Parallel()
	err := promptCreateError(errors.New("BadRequest"), &agent_yaml.PromptAgent{})
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.NotContains(t, localErr.Suggestion, "Responsible AI policy")
}
