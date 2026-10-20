// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"fmt"
	"slices"
)

// DeploymentSelection contains effective selections from the standard Foundry template.
type DeploymentSelection struct {
	Models         bool
	PrincipalID    string
	PrincipalType  string
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
	if selected.PrincipalID != "" {
		switch selected.PrincipalType {
		case "User", "Group", "ServicePrincipal", "ForeignGroup", "Device":
		default:
			return plan, fmt.Errorf("P09: effective developer principalType must identify a supported Entra principal")
		}
		add(Operation{
			CheckID: "P09", Action: roleAssignmentWrite, Scope: parent,
			RoleID:      "a97b65f3-24c7-4388-baec-2e87135dc908",
			PrincipalID: selected.PrincipalID, PrincipalType: selected.PrincipalType,
		})
	}
	return plan, nil
}
