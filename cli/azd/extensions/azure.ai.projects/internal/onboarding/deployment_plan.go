// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import "slices"

// DeploymentSelection contains effective selections from the standard Foundry template.
type DeploymentSelection struct {
	Models         bool
	NetworkChanges bool
}

// WithDeploymentOperations binds selected standard-template writes to existing ancestors.
func WithDeploymentOperations(plan Plan, selected DeploymentSelection) (Plan, error) {
	plan.Operations = slices.Clone(plan.Operations)
	add := func(op Operation) {
		if !slices.Contains(plan.Operations, op) {
			plan.Operations = append(plan.Operations, op)
		}
	}
	parent := plan.Hosting.Parent
	for _, action := range []string{
		"Microsoft.CognitiveServices/accounts/read", "Microsoft.CognitiveServices/accounts/projects/read",
	} {
		add(Operation{CheckID: "P09", Scope: parent, Action: action})
	}
	add(Operation{CheckID: "P09", Scope: parent,
		Action: "Microsoft.CognitiveServices/accounts/projects/connections/write"})
	if selected.Models {
		add(Operation{CheckID: "P09", Scope: parent, Action: "Microsoft.CognitiveServices/accounts/deployments/write"})
	}
	for _, scope := range []string{parent} {
		for _, action := range []string{"write", "read", "operations/read"} {
			add(Operation{CheckID: "P09", Scope: scope, Action: "Microsoft.Resources/deployments/" + action})
		}
	}
	return plan, nil
}
