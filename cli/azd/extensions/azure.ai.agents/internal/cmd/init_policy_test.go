// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/foundrypolicy"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubInitTokenCred struct{}

func (stubInitTokenCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token"}, nil
}

func TestWarnInitPolicyIfBlocked(t *testing.T) {
	origEval := initPolicyEvaluateCandidate
	origOut := initPolicyOut
	t.Cleanup(func() {
		initPolicyEvaluateCandidate = origEval
		initPolicyOut = origOut
	})

	proj := &FoundryProjectInfo{
		SubscriptionId:    "1756abc0-3554-4341-8d6a-46674962ea19",
		ResourceGroupName: "rg",
		AccountName:       "acct",
		ProjectName:       "proj",
	}
	pa := &agent_yaml.PromptAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindPrompt,
			Name: "my-prompt-agent",
		},
		Model:        "gpt-4.1",
		Instructions: "Help users.",
	}
	dep := &project.Deployment{
		Name: "gpt-4.1",
		Model: project.DeploymentModel{
			Name:    "gpt-4.1",
			Format:  "OpenAI",
			Version: "2025-04-14",
		},
	}

	t.Run("prints non-blocking warning when policy blocks agent kind or model", func(t *testing.T) {
		var buf bytes.Buffer
		initPolicyOut = &buf

		var captured foundrypolicy.Candidate
		initPolicyEvaluateCandidate = func(
			_ context.Context,
			_ azcore.TokenCredential,
			cand foundrypolicy.Candidate,
		) (foundrypolicy.EvaluationResult, error) {
			captured = cand
			return foundrypolicy.EvaluationResult{
				Outcome: foundrypolicy.OutcomeBlocked,
				Violations: []foundrypolicy.Violation{
					{
						Target:       foundrypolicy.TargetAgentKind,
						TargetValue:  "prompt",
						AssignmentID: "/subscriptions/sub/policyassignments/block-prompt",
						DisplayName:  "Block Prompt Agents",
						PortalURL:    "https://ai.azure.com/nextgen/r/test/manage/govern/assignments/abc",
						Message:      `The "Block Prompt Agents" policy doesn't allow prompt agent creation in this project.`,
					},
				},
			}, nil
		}

		warnInitPromptPolicyIfBlocked(t.Context(), stubInitTokenCred{}, proj, pa, dep)

		assert.Equal(t, "prompt", captured.AgentKind)
		require.Len(t, captured.Models, 1, "sparse manifest model and selected deployment must be merged")
		assert.Equal(t, "gpt-4.1", captured.Models[0].DeploymentName)
		assert.Equal(t, "gpt-4.1", captured.Models[0].Name)
		assert.Equal(t, "OpenAI", captured.Models[0].Format)
		assert.Equal(t, "2025-04-14", captured.Models[0].Version)
		outputText := buf.String()
		assert.Contains(t, outputText, "Warning: This agent configuration may be blocked by Azure Policy")
		assert.Contains(t, outputText, "Block Prompt Agents")
		assert.Contains(t, outputText, "View policy: https://ai.azure.com/nextgen/r/test/manage/govern/assignments/abc")
	})

	t.Run("fails open silently when policy evaluation returns an error", func(t *testing.T) {
		var buf bytes.Buffer
		initPolicyOut = &buf
		initPolicyEvaluateCandidate = func(
			_ context.Context,
			_ azcore.TokenCredential,
			_ foundrypolicy.Candidate,
		) (foundrypolicy.EvaluationResult, error) {
			return foundrypolicy.EvaluationResult{}, errors.New("Resource Graph timeout")
		}

		warnInitPromptPolicyIfBlocked(t.Context(), stubInitTokenCred{}, proj, pa, dep)
		assert.Empty(t, buf.String())
	})

	t.Run("skips check when creating a new project", func(t *testing.T) {
		var buf bytes.Buffer
		initPolicyOut = &buf
		called := false
		initPolicyEvaluateCandidate = func(
			_ context.Context,
			_ azcore.TokenCredential,
			_ foundrypolicy.Candidate,
		) (foundrypolicy.EvaluationResult, error) {
			called = true
			return foundrypolicy.EvaluationResult{Outcome: foundrypolicy.OutcomeAllowed}, nil
		}

		warnInitPromptPolicyIfBlocked(t.Context(), stubInitTokenCred{}, nil, pa, dep)
		assert.False(t, called)
		assert.Empty(t, buf.String())
	})
}
