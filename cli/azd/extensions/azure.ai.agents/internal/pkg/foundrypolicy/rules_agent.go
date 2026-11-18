// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundrypolicy

import "github.com/azure/azure-dev/cli/shared/foundry/policy"

const (
	// DeniedAgentKindsPolicyID is the built-in Azure Policy definition ID for denied Foundry agent kinds.
	DeniedAgentKindsPolicyID = policy.DeniedAgentKindsPolicyID
	// AllowedAgentKindsPolicyID is the built-in Azure Policy definition ID for allowed Foundry agent kinds.
	AllowedAgentKindsPolicyID = policy.AllowedAgentKindsPolicyID

	// DeniedAgentKindsDisplaySuffix is the display-name suffix for the denied agent kinds policy.
	DeniedAgentKindsDisplaySuffix = policy.DeniedAgentKindsDisplaySuffix
	// AllowedAgentKindsDisplaySuffix is the display-name suffix for the allowed agent kinds policy.
	AllowedAgentKindsDisplaySuffix = policy.AllowedAgentKindsDisplaySuffix
)

// NormalizeAgentKindForPolicy normalizes an authoring agent kind to the wire kind
// evaluated by Azure Policy (e.g., "prompt-voice" -> "voice").
func NormalizeAgentKindForPolicy(agentKind string) string {
	return policy.NormalizeAgentKindForPolicy(agentKind)
}

// IsAgentKindBlocked reports whether assignment blocks agentKind on projectID.
func IsAgentKindBlocked(assignment PolicyAssignment, projectID, agentKind string) bool {
	return policy.IsAgentKindBlocked(assignment, projectID, agentKind)
}

// FindBlockingAgentKindPolicies returns all assignments blocking agentKind on projectID,
// ordered deterministically by assignment ID.
func FindBlockingAgentKindPolicies(
	assignments []PolicyAssignment,
	projectID string,
	agentKind string,
) []PolicyAssignment {
	return policy.FindBlockingAgentKindPolicies(assignments, projectID, agentKind)
}

// EvaluateAgentKindPolicy evaluates inventory against agentKind and returns the structured result.
func EvaluateAgentKindPolicy(inventory Inventory, agentKind string) EvaluationResult {
	return policy.EvaluateAgentKindPolicy(inventory, agentKind)
}
