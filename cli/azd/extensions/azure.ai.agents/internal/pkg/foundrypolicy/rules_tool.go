// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundrypolicy

import "github.com/azure/azure-dev/cli/shared/foundry/policy"

const (
	// DeniedToolsPolicyID is the built-in Azure Policy definition ID for denied Foundry tools.
	DeniedToolsPolicyID = policy.DeniedToolsPolicyID
	// AllowedToolsPolicyID is the built-in Azure Policy definition ID for approved Foundry tools.
	AllowedToolsPolicyID = policy.AllowedToolsPolicyID

	// DeniedToolsDisplaySuffix is the display-name suffix for denied Foundry agent and toolbox tools.
	DeniedToolsDisplaySuffix = policy.DeniedToolsDisplaySuffix
	// LegacyDeniedToolsDisplaySuffix is the legacy display-name suffix for denied Foundry agent tools.
	LegacyDeniedToolsDisplaySuffix = policy.LegacyDeniedToolsDisplaySuffix
	// AllowedToolsDisplaySuffix is the display-name suffix for approved Foundry agent and toolbox tools.
	AllowedToolsDisplaySuffix = policy.AllowedToolsDisplaySuffix
	// LegacyAllowedToolsDisplaySuffix is the legacy display-name suffix for allowed Foundry agent tools.
	LegacyAllowedToolsDisplaySuffix = policy.LegacyAllowedToolsDisplaySuffix
)

// ToolIdentifiers returns the normalized policy identifiers for legacy `deniedTools` / `allowedTools`
// matching.
func ToolIdentifiers(tool ToolCandidate) []string {
	return policy.ToolIdentifiers(tool)
}

// EvaluateToolPolicyAssignment evaluates a single policy assignment against a tool candidate.
func EvaluateToolPolicyAssignment(
	assignment PolicyAssignment,
	projectID string,
	tool ToolCandidate,
) EvaluationOutcome {
	return policy.EvaluateToolPolicyAssignment(assignment, projectID, tool)
}

// IsToolBlocked reports whether assignment blocks the given tool candidate on projectID.
func IsToolBlocked(assignment PolicyAssignment, projectID string, tool ToolCandidate) bool {
	return policy.IsToolBlocked(assignment, projectID, tool)
}

// EvaluateToolPolicy evaluates inventory against all tools configured on an agent.
func EvaluateToolPolicy(inventory Inventory, tools []ToolCandidate) EvaluationResult {
	return policy.EvaluateToolPolicy(inventory, tools)
}
