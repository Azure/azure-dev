// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"
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
				g.warnf("could not verify Responsible AI policies: %v", err)
				return nil
			}
			var dropped []int
			for _, i := range declared {
				ref, ok := azure.ParseRaiPolicyResourceID(g.managed.Policies[i].RaiPolicyName)
				if !ok {
					// The agent node's ValidatePolicies already rejected this
					// shape; guard rather than issue a nonsense request.
					continue
				}
				existing, err := lister(ctx, ref)
				if err != nil {
					// A missing read permission must not block a deploy that
					// would otherwise succeed: the service is still the
					// authority on whether the policy is usable.
					g.warnf(
						"could not verify Responsible AI policy %q on account %q: %v",
						ref.PolicyName, ref.AccountName, err,
					)
					continue
				}
				if raiPolicyPresent(existing, ref.PolicyName) {
					continue
				}

				fallback, ok := defaultRaiPolicy(existing)
				if !ok {
					dropped = append(dropped, i)
					g.warnf(
						"Responsible AI policy %q was not found on Foundry account %q and the account "+
							"carries no built-in policy to fall back to; publishing without guardrails, "+
							"which leaves the account's default content filters in force. Point "+
							"policies[].raiPolicyName in agent.yaml at a policy that exists and redeploy.",
						ref.PolicyName, ref.AccountName,
					)
					continue
				}
				g.managed.Policies[i].RaiPolicyName = fallback.ResourceID
				g.warnf(
					"Responsible AI policy %q was not found on Foundry account %q; using the built-in "+
						"%q instead. Point policies[].raiPolicyName in agent.yaml at the policy you "+
						"want and redeploy, or create it with: az cognitiveservices account rai-policy "+
						"create --name %s --resource-group %s --rai-policy-name %s",
					ref.PolicyName, ref.AccountName, fallback.Name,
					ref.AccountName, ref.ResourceGroup, ref.PolicyName,
				)
			}
			if len(dropped) > 0 {
				kept := make([]agent_yaml.Policy, 0, len(g.managed.Policies))
				for i, policy := range g.managed.Policies {
					if slices.Contains(dropped, i) {
						continue
					}
					kept = append(kept, policy)
				}
				g.managed.Policies = kept
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

// defaultRaiPolicy picks the policy to substitute when the declared one is not
// on the account.
//
// Only the service-supplied built-ins are eligible. Attaching a policy someone
// else authored would apply content filters nobody asked for and that azd
// cannot reason about, whereas the built-ins are the same filters the account
// already applies to an agent that names no policy.
func defaultRaiPolicy(policies []azure.RaiPolicyInfo) (azure.RaiPolicyInfo, bool) {
	builtIn := make([]azure.RaiPolicyInfo, 0, len(policies))
	for _, policy := range policies {
		if policy.SystemManaged {
			builtIn = append(builtIn, policy)
		}
	}
	if len(builtIn) == 0 {
		return azure.RaiPolicyInfo{}, false
	}
	// Newest built-in first, so an account carrying both lands on the current
	// defaults. Ties break by name so the choice does not vary with the order
	// the service happened to return.
	slices.SortFunc(builtIn, func(a, b azure.RaiPolicyInfo) int {
		if rank := cmp.Compare(raiPolicyRank(a.Name), raiPolicyRank(b.Name)); rank != 0 {
			return rank
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return builtIn[0], true
}

// raiPolicyRank orders the service's built-in policies newest first. Anything
// unrecognized sorts last rather than being excluded, so an account whose
// built-ins are renamed still yields a fallback.
func raiPolicyRank(name string) int {
	switch {
	case strings.EqualFold(name, "Microsoft.DefaultV2"):
		return 0
	case strings.EqualFold(name, "Microsoft.Default"):
		return 1
	default:
		return 2
	}
}

// azureRaiPolicyLister lists the account's policies from ARM using the same
// credential the rest of the prompt deploy path uses.
func azureRaiPolicyLister() (raiPolicyLister, error) {
	credential := promptCredential()
	if credential == nil {
		return nil, fmt.Errorf("no Azure credential is available")
	}
	return func(ctx context.Context, ref azure.RaiPolicyRef) ([]azure.RaiPolicyInfo, error) {
		return azure.ListRaiPolicies(ctx, credential, ref.SubscriptionID, ref.ResourceGroup, ref.AccountName)
	}, nil
}
