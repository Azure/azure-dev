// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// raiPolicyLister returns every Responsible AI policy on the account named by
// ref. Listing rather than probing one name at a time answers both of the
// node's questions from a single call: whether the declared policy is present,
// and what to fall back to when it is not. It exists as a function type so the
// node can be exercised without an Azure call.
type raiPolicyLister func(ctx context.Context, ref azure.RaiPolicyRef) ([]azure.RaiPolicyInfo, error)

// policiesNode reconciles every declared Responsible AI policy against the
// account before the agent version is published.
//
// The create call reports a policy the account does not have as a generic bad
// request that names neither the policy nor the account, so a typo or a
// forgotten `azd provision` surfaces as an opaque service rejection at the very
// end of a deploy. Checking here names the policy and the account it was looked
// for on.
//
// A policy that is absent is a warning, not a failure. The declared name is the
// author's preference, not a safety floor: the account applies its own default
// content filters to an agent that names no policy at all, so falling back to
// the built-in default leaves the agent no less filtered than publishing it
// without guardrails would. Failing instead would block a deploy over a value
// that is trivially editable afterwards, and the substitution is reported so
// the author can point policies[].raiPolicyName somewhere else and redeploy.
//
// Returns nil when the agent declares no RAI policy, so the deploy path is
// unchanged for the agents that do not use one.
func policiesNode(g *promptGraph, newLister func() (raiPolicyLister, error)) *promptNode {
	var declared []int
	for i, policy := range g.managed.Policies {
		if policy.Type != agent_yaml.PolicyTypeRai {
			continue
		}
		if strings.TrimSpace(policy.RaiPolicyName) != "" {
			declared = append(declared, i)
		}
	}
	if len(declared) == 0 {
		return nil
	}

	names := make([]string, 0, len(declared))
	for _, i := range declared {
		names = append(names, strings.TrimSpace(g.managed.Policies[i].RaiPolicyName))
	}

	return &promptNode{
		Kind: nodePolicy,
		ID:   strings.Join(names, ","),
		// Shape validation already runs on the agent node through
		// ValidatePolicies; nothing further is knowable without a live call.
		Validate: func() error { return nil },
		Resolve: func(ctx context.Context) error {
			lister, err := newLister()
			if err != nil {
				return fmt.Errorf("creating Responsible AI policy client: %w", err)
			}
			for _, i := range declared {
				ref, ok := azure.ParseRaiPolicyResourceID(g.managed.Policies[i].RaiPolicyName)
				if !ok {
					// The agent node's ValidatePolicies already rejected this
					// shape; guard rather than issue a nonsense request.
					continue
				}
				existing, err := lister(ctx, ref)
				if err != nil {
					return fmt.Errorf(
						"verifying Responsible AI policy %q on account %q: %w",
						ref.PolicyName, ref.AccountName, err)
				}
				if raiPolicyPresent(existing, ref.PolicyName) {
					continue
				}
				return exterrors.Dependency(
					exterrors.CodeRaiPolicyNotFound,
					fmt.Sprintf("Responsible AI policy %q was not found on Foundry account %q",
						ref.PolicyName, ref.AccountName),
					"create the policy or update policies[].raiPolicyName to an existing policy resource ID",
				)
			}
			return nil
		},
	}
}

// raiPolicyPresent reports whether the account carries a policy by this name.
// The comparison is case-insensitive because ARM echoes resource IDs back with
// the casing the caller used, so a hand-copied ID may disagree with the
// service's own casing without naming a different policy.
func raiPolicyPresent(policies []azure.RaiPolicyInfo, name string) bool {
	return slices.ContainsFunc(policies, func(policy azure.RaiPolicyInfo) bool {
		return strings.EqualFold(policy.Name, name)
	})
}

// azureRaiPolicyLister lists the account's policies from ARM using the same
// credential the rest of the prompt deploy path uses.
func azureRaiPolicyLister(credential azcore.TokenCredential) (raiPolicyLister, error) {
	if credential == nil {
		return nil, fmt.Errorf("no Azure credential is available")
	}
	return func(ctx context.Context, ref azure.RaiPolicyRef) ([]azure.RaiPolicyInfo, error) {
		return azure.ListRaiPolicies(ctx, credential, ref.SubscriptionID, ref.ResourceGroup, ref.AccountName)
	}, nil
}
