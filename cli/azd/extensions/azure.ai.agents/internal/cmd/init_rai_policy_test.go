// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/stretchr/testify/require"
)

const testRaiPolicyID = "/subscriptions/sub-1/resourceGroups/my-rg/providers/" +
	"Microsoft.CognitiveServices/accounts/my-account/raiPolicies/strict"

func testFoundryProject() *FoundryProjectInfo {
	return &FoundryProjectInfo{
		SubscriptionId:    "sub-1",
		ResourceGroupName: "my-rg",
		AccountName:       "my-account",
		ProjectName:       "my-project",
	}
}

// TestResolvePromptRaiPolicyFlags covers the non-interactive selections, which
// are the only ones a scripted init can take.
func TestResolvePromptRaiPolicyFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   initFlags
		project *FoundryProjectInfo
		want    raiPolicySelection
	}{
		{
			name:  "none detaches",
			flags: initFlags{raiPolicy: "none"},
			want:  raiPolicySelection{},
		},
		{
			name:  "none is case insensitive",
			flags: initFlags{raiPolicy: "NONE"},
			want:  raiPolicySelection{},
		},
		{
			name:  "full resource id is used verbatim",
			flags: initFlags{raiPolicy: testRaiPolicyID},
			want: raiPolicySelection{
				Attached: true, ResourceID: testRaiPolicyID, PolicyName: "strict",
			},
		},
		{
			name:    "short name resolves against the selected account",
			flags:   initFlags{raiPolicy: "strict"},
			project: testFoundryProject(),
			want: raiPolicySelection{
				Attached: true, ResourceID: testRaiPolicyID, PolicyName: "strict",
			},
		},
		{
			// Nothing is attached rather than something being guessed: a policy
			// the caller did not ask for changes how the agent answers.
			name:  "no flag with no prompt attaches nothing",
			flags: initFlags{noPrompt: true},
			want:  raiPolicySelection{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolvePromptRaiPolicy(t.Context(), nil, &test.flags, nil, test.project, nil)
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

// TestResolvePromptRaiPolicyShortNameWithoutAccount verifies a name that cannot
// be resolved fails with a message that names the fix, rather than producing a
// malformed ID the service would reject much later.
func TestResolvePromptRaiPolicyShortNameWithoutAccount(t *testing.T) {
	t.Parallel()

	_, err := resolvePromptRaiPolicy(
		t.Context(), nil, &initFlags{raiPolicy: "strict"}, nil, nil, nil,
	)
	require.ErrorContains(t, err, "no existing Foundry account was selected")
}

// TestResolvePromptRaiPolicyManifestWins verifies an authored policy set is not
// second-guessed: init neither prompts nor overwrites it.
func TestResolvePromptRaiPolicyManifestWins(t *testing.T) {
	t.Parallel()

	manifest := &promptAgentManifest{
		definition: agent_yaml.PromptAgent{
			Policies: []agent_yaml.Policy{
				{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: testRaiPolicyID},
			},
		},
	}

	got, err := resolvePromptRaiPolicy(
		t.Context(), nil, &initFlags{raiPolicy: "none"}, manifest, testFoundryProject(), nil,
	)
	require.NoError(t, err)
	require.Equal(t, raiPolicySelection{}, got)
}

// TestApplyRaiPolicySelectionDetached verifies the no-policy choice leaves the
// scaffold untouched, so existing behavior is unchanged for agents that do not
// use guardrails.
func TestApplyRaiPolicySelectionDetached(t *testing.T) {
	t.Parallel()

	promptAgent := agent_yaml.PromptAgent{}
	require.NoError(t, applyRaiPolicySelection(
		t.Context(), nil, "dev", &promptAgent, raiPolicySelection{},
	))
	require.Empty(t, promptAgent.Policies)
}

// TestPromptForRaiPolicyWithoutAccount verifies init does not prompt when there
// is no account to enumerate. azd cannot create a policy, so a picker whose
// only entry is "no policy" would ask a question with one possible answer.
func TestPromptForRaiPolicyWithoutAccount(t *testing.T) {
	t.Parallel()

	// A nil client would panic if the picker were reached.
	got, err := promptForRaiPolicy(t.Context(), nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, raiPolicySelection{}, got)
}
