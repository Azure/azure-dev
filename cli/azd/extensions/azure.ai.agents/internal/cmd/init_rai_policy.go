// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
)

const (
	// raiPolicyEnvVar names the azd environment variable holding the Responsible
	// AI policy's full ARM resource ID.
	//
	// agent.yaml references the variable rather than the ID itself: the ID
	// embeds a subscription, resource group and account, so writing it literally
	// would pin the scaffold to the machine that ran init. This mirrors how the
	// promptAgent block in azure.yaml already states its Foundry target.
	raiPolicyEnvVar = "RAI_POLICY_ID"

	// raiPolicyRef is the value written into agent.yaml's policies[].raiPolicyName.
	raiPolicyRef = "${" + raiPolicyEnvVar + "}"

	// raiPolicyFlagNone is the only symbolic --rai-policy value; anything else
	// is a policy name or a full ARM resource ID.
	raiPolicyFlagNone = "none"
)

// raiPolicySelection is the outcome of resolving a Responsible AI policy for a
// freshly scaffolded prompt or managed agent.
//
// Attached is false for the default "no policy" choice, in which case the agent
// inherits the account's default content filters and nothing is written.
type raiPolicySelection struct {
	// Attached reports whether agent.yaml should declare a policies[] entry.
	Attached bool
	// ResourceID is the concrete ARM resource ID to record in the azd
	// environment.
	ResourceID string
	// PolicyName is the policy's short name, used for display.
	PolicyName string
}

// resolvePromptRaiPolicy decides which Responsible AI policy a scaffolded
// prompt or managed agent binds to.
//
// azd attaches an existing policy; it does not create one. A policy is an
// account-scoped compliance resource that is frequently shared across agents
// and owned by a different team than the one scaffolding this project, so
// creating one as a side effect of init would be presumptuous. The docs carry
// a worked example for authors who do want to provision one themselves.
//
// A manifest that already declares policies wins outright: the author stated
// their guardrails and init must not second-guess them. Otherwise --rai-policy
// selects non-interactively, and an interactive run lists the policies that
// already exist on the target Foundry account so the common case is a pick
// rather than a resource ID the developer has to go and look up.
//
// foundryProject is nil when init is going to create a new Foundry project, in
// which case there is no account to enumerate.
func resolvePromptRaiPolicy(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
	manifest *promptAgentManifest,
	foundryProject *FoundryProjectInfo,
	credential azcore.TokenCredential,
) (raiPolicySelection, error) {
	if manifest != nil && len(manifest.definition.Policies) > 0 {
		return raiPolicySelection{}, nil
	}

	requested := strings.TrimSpace(flags.raiPolicy)
	switch {
	case strings.EqualFold(requested, raiPolicyFlagNone):
		return raiPolicySelection{}, nil
	case requested != "":
		return raiPolicySelectionFromFlag(requested, foundryProject)
	}

	// No policy is the safe default for a non-interactive run: the account's
	// own default filters still apply, and attaching a policy the caller did
	// not ask for would change how the agent answers.
	if flags.noPrompt {
		return raiPolicySelection{}, nil
	}

	return promptForRaiPolicy(ctx, azdClient, foundryProject, credential)
}

// raiPolicySelectionFromFlag interprets a non-symbolic --rai-policy value as
// either a full ARM resource ID or a policy name on the resolved account.
func raiPolicySelectionFromFlag(
	requested string,
	foundryProject *FoundryProjectInfo,
) (raiPolicySelection, error) {
	if ref, ok := azure.ParseRaiPolicyResourceID(requested); ok {
		return raiPolicySelection{
			Attached:   true,
			ResourceID: requested,
			PolicyName: ref.PolicyName,
		}, nil
	}

	if foundryProject == nil {
		return raiPolicySelection{}, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("--rai-policy %q is a policy name, but no existing Foundry account was selected", requested),
			"pass the policy's full ARM resource ID, or select an existing Foundry project "+
				"so the name can be resolved against its account",
		)
	}

	return raiPolicySelection{
		Attached: true,
		ResourceID: azure.RaiPolicyResourceID(
			foundryProject.SubscriptionId,
			foundryProject.ResourceGroupName,
			foundryProject.AccountName,
			requested,
		),
		PolicyName: requested,
	}, nil
}

// promptForRaiPolicy asks the developer to pick a policy, listing the ones that
// already exist on the account.
//
// A failure to list is not fatal. Reading RAI policies needs a role the
// developer may not hold, and a missing list should cost them the convenience
// of a picker rather than the ability to finish init.
func promptForRaiPolicy(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	foundryProject *FoundryProjectInfo,
	credential azcore.TokenCredential,
) (raiPolicySelection, error) {
	var existing []azure.RaiPolicyInfo
	if foundryProject != nil && credential != nil {
		policies, err := azure.ListRaiPolicies(
			ctx, credential,
			foundryProject.SubscriptionId,
			foundryProject.ResourceGroupName,
			foundryProject.AccountName,
		)
		if err != nil {
			fmt.Println(color.HiBlackString(
				"Could not list Responsible AI policies on %q: %v", foundryProject.AccountName, err,
			))
		} else {
			existing = policies
		}
	}

	// Nothing to choose between. Showing a one-option picker whose only answer
	// is the default wastes a prompt, and azd has no policy to offer to create.
	if len(existing) == 0 {
		return raiPolicySelection{}, nil
	}

	choices := []*azdext.SelectChoice{{
		Label: "No Responsible AI policy (use the account's default content filters)",
		Value: raiPolicyFlagNone,
	}}
	for _, policy := range existing {
		label := policy.Name
		if policy.SystemManaged {
			label += " (built-in)"
		}
		if policy.BasePolicyName != "" {
			label += fmt.Sprintf(" - based on %s", policy.BasePolicyName)
		}
		choices = append(choices, &azdext.SelectChoice{Label: label, Value: policy.ResourceID})
	}

	resp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select a Responsible AI policy for this agent",
			Choices: choices,
			HelpMessage: "A Responsible AI policy applies content filters to the agent's prompts and " +
				"completions. The agent references the policy through " + raiPolicyRef + " in agent.yaml, " +
				"so the project stays portable across subscriptions. Create a policy with " +
				"`az cognitiveservices account rai-policy create` and re-run to see it here.",
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return raiPolicySelection{}, exterrors.Cancelled("Responsible AI policy selection was cancelled")
		}
		return raiPolicySelection{}, exterrors.Dependency(
			exterrors.CodePromptFailed,
			fmt.Sprintf("failed to select a Responsible AI policy: %s", err),
			"pass --rai-policy none or --rai-policy <resource id> to skip the interactive selection",
		)
	}

	selected := int(*resp.Value)
	if selected <= 0 || selected > len(existing) {
		return raiPolicySelection{}, nil
	}

	policy := existing[selected-1]
	return raiPolicySelection{
		Attached:   true,
		ResourceID: policy.ResourceID,
		PolicyName: policy.Name,
	}, nil
}

// applyRaiPolicySelection records the selection on the scaffold: the agent
// declares the policy through the environment reference, and the concrete
// resource ID lands in the azd environment.
func applyRaiPolicySelection(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	promptAgent *agent_yaml.PromptAgent,
	selection raiPolicySelection,
) error {
	if !selection.Attached || selection.ResourceID == "" {
		return nil
	}

	promptAgent.Policies = []agent_yaml.Policy{{
		Type:          agent_yaml.PolicyTypeRai,
		RaiPolicyName: raiPolicyRef,
	}}

	if err := setEnvValue(ctx, azdClient, envName, raiPolicyEnvVar, selection.ResourceID); err != nil {
		return fmt.Errorf("recording %s: %w", raiPolicyEnvVar, err)
	}
	return nil
}
