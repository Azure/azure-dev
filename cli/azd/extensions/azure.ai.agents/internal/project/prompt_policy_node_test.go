// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// TestExpandPromptAgentPolicies verifies the ${RAI_POLICY_ID} indirection init
// writes resolves against the azd environment before anything validates the
// shape of the value.
func TestExpandPromptAgentPolicies(t *testing.T) {
	t.Parallel()

	managed := agent_yaml.PromptAgent{
		Policies: []agent_yaml.Policy{
			{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: "${RAI_POLICY_ID}"},
		},
	}

	require.NoError(t, expandPromptAgentPolicies(&managed, map[string]string{
		"RAI_POLICY_ID": raiPolicyID,
	}))
	require.Equal(t, raiPolicyID, managed.Policies[0].RaiPolicyName)

	// The expanded value must satisfy the shape check that runs later, or the
	// indirection would trade one confusing failure for another.
	require.NoError(t, managed.ValidatePolicies())
}

// TestExpandPromptAgentPoliciesUnresolved verifies an unset variable fails
// loudly. Publishing the agent without the guardrails its manifest declares
// would be worse than not publishing at all.
func TestExpandPromptAgentPoliciesUnresolved(t *testing.T) {
	t.Parallel()

	managed := agent_yaml.PromptAgent{
		Policies: []agent_yaml.Policy{
			{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: "${RAI_POLICY_ID}"},
		},
	}

	err := expandPromptAgentPolicies(&managed, map[string]string{})
	require.ErrorContains(t, err, "not set in the azd environment")

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Suggestion, "azd provision")
	require.Contains(t, localErr.Suggestion, "RAI_POLICY_ID")
}

// TestExpandPromptAgentPoliciesLeavesLiterals verifies a literal resource ID is
// untouched, so projects that already hard-code one keep working.
func TestExpandPromptAgentPoliciesLeavesLiterals(t *testing.T) {
	t.Parallel()

	managed := agent_yaml.PromptAgent{
		Policies: []agent_yaml.Policy{
			{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID},
		},
	}

	require.NoError(t, expandPromptAgentPolicies(&managed, nil))
	require.Equal(t, raiPolicyID, managed.Policies[0].RaiPolicyName)
}

// TestPromptAgentPoliciesReachRaiConfig is the prompt-agent counterpart to
// TestAgentPoliciesReachRaiConfig: a policy authored on a prompt or managed
// agent must arrive as rai_config.rai_policy_name on the managed definition.
func TestPromptAgentPoliciesReachRaiConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		harness *agent_yaml.PromptHarness
	}{
		{name: "prompt agent"},
		{
			name:    "managed agent",
			harness: &agent_yaml.PromptHarness{Type: agent_api.ManagedAgentHarnessGitHubCopilot},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			managed := agent_yaml.PromptAgent{
				AgentDefinition: agent_yaml.AgentDefinition{
					Name: "rai-agent",
					Kind: agent_yaml.AgentKindPrompt,
				},
				Model:        "gpt-4.1-mini",
				Instructions: "be helpful",
				Harness:      test.harness,
				Policies: []agent_yaml.Policy{
					{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID},
				},
			}

			request, err := agent_yaml.CreatePromptAgentAPIRequest(managed, nil)
			require.NoError(t, err)

			definition, ok := request.Definition.(agent_api.ManagedAgentDefinition)
			require.True(t, ok)
			require.NotNil(t, definition.RaiConfig)
			require.Equal(t, raiPolicyID, definition.RaiConfig.RaiPolicyName)
		})
	}
}

// TestPoliciesNodeAbsent verifies the deploy path is untouched for agents that
// declare no policy.
func TestPoliciesNodeAbsent(t *testing.T) {
	t.Parallel()

	g := &promptGraph{managed: &agent_yaml.PromptAgent{}}
	require.Nil(t, policiesNode(g, nil))
}

// TestPoliciesNodeMissingPolicyFallsBack verifies a policy the account does not
// have is replaced with the account's built-in default and reported, rather
// than failing a deploy over a value the author can edit afterwards.
func TestPoliciesNodeMissingPolicyFallsBack(t *testing.T) {
	t.Parallel()

	g := &promptGraph{managed: &agent_yaml.PromptAgent{
		Policies: []agent_yaml.Policy{
			{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID},
		},
	}}
	var warnings []string
	g.warn = func(message string) { warnings = append(warnings, message) }

	const defaultID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/" +
		"my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/raiPolicies/Microsoft.Default"

	node := policiesNode(g, func() (raiPolicyLister, error) {
		return func(context.Context, azure.RaiPolicyRef) ([]azure.RaiPolicyInfo, error) {
			return []azure.RaiPolicyInfo{
				{Name: "team-strict", ResourceID: "/custom"},
				{Name: "Microsoft.Default", ResourceID: defaultID, SystemManaged: true},
			}, nil
		}, nil
	})
	require.NotNil(t, node)
	require.NoError(t, node.Validate())
	require.NoError(t, node.Resolve(t.Context()))

	// The agent keeps a guardrail, and it is the built-in rather than the
	// custom policy that happened to be on the account.
	require.Equal(t, defaultID, g.managed.Policies[0].RaiPolicyName)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "Microsoft.DefaultV2")
	require.Contains(t, warnings[0], "my-account")
	require.Contains(t, warnings[0], "Microsoft.Default")
}

// TestPoliciesNodeMissingPolicyWithoutFallback verifies an account carrying no
// built-in policy publishes without guardrails instead of failing. The
// account's own default content filters still apply.
func TestPoliciesNodeMissingPolicyWithoutFallback(t *testing.T) {
	t.Parallel()

	g := &promptGraph{managed: &agent_yaml.PromptAgent{
		Policies: []agent_yaml.Policy{
			{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID},
		},
	}}
	var warnings []string
	g.warn = func(message string) { warnings = append(warnings, message) }

	node := policiesNode(g, func() (raiPolicyLister, error) {
		return func(context.Context, azure.RaiPolicyRef) ([]azure.RaiPolicyInfo, error) {
			return nil, nil
		}, nil
	})
	require.NotNil(t, node)
	require.NoError(t, node.Resolve(t.Context()))

	require.Empty(t, g.managed.Policies)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "Microsoft.DefaultV2")
}

// TestPoliciesNodePresentPolicy verifies a policy that exists resolves cleanly
// and is left alone.
func TestPoliciesNodePresentPolicy(t *testing.T) {
	t.Parallel()

	g := &promptGraph{managed: &agent_yaml.PromptAgent{
		Policies: []agent_yaml.Policy{
			{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID},
		},
	}}

	node := policiesNode(g, func() (raiPolicyLister, error) {
		return func(context.Context, azure.RaiPolicyRef) ([]azure.RaiPolicyInfo, error) {
			// Casing differs from the declared ID: ARM echoes back whatever the
			// caller used, so this must not read as a different policy.
			return []azure.RaiPolicyInfo{{Name: "microsoft.defaultv2", SystemManaged: true}}, nil
		}, nil
	})
	require.NotNil(t, node)
	require.NoError(t, node.Resolve(t.Context()))
	require.Equal(t, raiPolicyID, g.managed.Policies[0].RaiPolicyName)
}

// TestPoliciesNodeLookupFailureIsNotFatal verifies a developer without the role
// to read policies can still deploy: the service remains the authority on
// whether the policy is usable.
func TestPoliciesNodeLookupFailureIsNotFatal(t *testing.T) {
	t.Parallel()

	g := &promptGraph{managed: &agent_yaml.PromptAgent{
		Policies: []agent_yaml.Policy{
			{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID},
		},
	}}

	node := policiesNode(g, func() (raiPolicyLister, error) {
		return func(context.Context, azure.RaiPolicyRef) ([]azure.RaiPolicyInfo, error) {
			return nil, errors.New("authorization failed")
		}, nil
	})
	require.NotNil(t, node)
	require.NoError(t, node.Resolve(t.Context()))

	// The declared policy is left in place: the service is still the authority
	// on whether it is usable, and azd could not read the account to know
	// otherwise.
	require.Equal(t, raiPolicyID, g.managed.Policies[0].RaiPolicyName)
}

// TestPromptCreateErrorAddsPolicySuggestion verifies a failed create on an agent
// with guardrails points at rai_config, which the service's own message does
// not mention.
func TestPromptCreateErrorAddsPolicySuggestion(t *testing.T) {
	t.Parallel()

	managed := agent_yaml.PromptAgent{
		Harness: &agent_yaml.PromptHarness{Type: agent_api.ManagedAgentHarnessGitHubCopilot},
		Policies: []agent_yaml.Policy{
			{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID},
		},
	}

	err := promptCreateError(errors.New("BadRequest"), &managed)
	require.ErrorContains(t, err, "BadRequest")

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Suggestion, "Responsible AI policy")
	require.Contains(t, localErr.Suggestion, "harness")
}

// TestPromptCreateErrorWithoutPolicyIsUnchanged verifies agents without
// guardrails keep the existing service error verbatim.
func TestPromptCreateErrorWithoutPolicyIsUnchanged(t *testing.T) {
	t.Parallel()

	err := promptCreateError(errors.New("BadRequest"), &agent_yaml.PromptAgent{})

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.NotContains(t, localErr.Suggestion, "Responsible AI policy")
}
