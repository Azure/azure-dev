// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundrypolicy

import (
	"fmt"
	"slices"
	"strings"
)

const (
	// DeniedAgentKindsPolicyID is the built-in Azure Policy definition ID for denied Foundry agent kinds.
	DeniedAgentKindsPolicyID = "/providers/microsoft.authorization/policydefinitions/" +
		"b6ca2367-8597-4a3f-ab19-48f58d6d9278"
	// AllowedAgentKindsPolicyID is the built-in Azure Policy definition ID for allowed Foundry agent kinds.
	AllowedAgentKindsPolicyID = "/providers/microsoft.authorization/policydefinitions/" +
		"85455646-5d02-433e-a42d-265151140427"

	// DeniedAgentKindsDisplaySuffix is the display-name suffix for the denied agent kinds policy.
	DeniedAgentKindsDisplaySuffix = "microsoft foundry agents should not use denied agent kinds"
	// AllowedAgentKindsDisplaySuffix is the display-name suffix for the allowed agent kinds policy.
	AllowedAgentKindsDisplaySuffix = "microsoft foundry agents should only use allowed agent kinds"
)

var (
	deniedAgentKindsSpec = PolicyDefinitionSpec{
		DefinitionID:      DeniedAgentKindsPolicyID,
		DisplayNameSuffix: DeniedAgentKindsDisplaySuffix,
		ParameterName:     "deniedAgentKinds",
		IsDenyList:        true,
	}
	allowedAgentKindsSpec = PolicyDefinitionSpec{
		DefinitionID:      AllowedAgentKindsPolicyID,
		DisplayNameSuffix: AllowedAgentKindsDisplaySuffix,
		ParameterName:     "allowedAgentKinds",
		IsDenyList:        false,
	}
)

// NormalizeAgentKindForPolicy normalizes an authoring agent kind to the wire kind
// evaluated by Azure Policy (e.g., "prompt-voice" -> "voice").
func NormalizeAgentKindForPolicy(agentKind string) string {
	normalized := strings.ToLower(strings.TrimSpace(agentKind))
	if normalized == "prompt-voice" {
		return "voice"
	}
	return normalized
}

// IsAgentKindBlocked reports whether assignment blocks agentKind on projectID.
func IsAgentKindBlocked(assignment PolicyAssignment, projectID, agentKind string) bool {
	if !IsPolicyAssignmentApplicable(assignment, projectID) {
		return false
	}
	normalizedKind := NormalizeAgentKindForPolicy(agentKind)
	if normalizedKind == "" {
		return false
	}

	if MatchesPolicyDefinition(assignment, deniedAgentKindsSpec) {
		kinds, ok := EffectiveStringSliceParam(assignment, deniedAgentKindsSpec.ParameterName)
		if !ok {
			return false
		}
		return slices.ContainsFunc(kinds, func(item string) bool {
			return NormalizeAgentKindForPolicy(item) == normalizedKind
		})
	}

	if MatchesPolicyDefinition(assignment, allowedAgentKindsSpec) {
		kinds, ok := EffectiveStringSliceParam(assignment, allowedAgentKindsSpec.ParameterName)
		if !ok {
			return false
		}
		return !slices.ContainsFunc(kinds, func(item string) bool {
			return NormalizeAgentKindForPolicy(item) == normalizedKind
		})
	}

	return false
}

// FindBlockingAgentKindPolicies returns all assignments blocking agentKind on projectID,
// ordered deterministically by assignment ID.
func FindBlockingAgentKindPolicies(
	assignments []PolicyAssignment,
	projectID string,
	agentKind string,
) []PolicyAssignment {
	sorted := SortAssignmentsByID(assignments)
	var blocking []PolicyAssignment
	for _, assignment := range sorted {
		if IsAgentKindBlocked(assignment, projectID, agentKind) {
			blocking = append(blocking, assignment)
		}
	}
	return blocking
}

// EvaluateAgentKindPolicy evaluates inventory against agentKind and returns the structured result.
func EvaluateAgentKindPolicy(inventory Inventory, agentKind string) EvaluationResult {
	normalizedKind := NormalizeAgentKindForPolicy(agentKind)
	blocking := FindBlockingAgentKindPolicies(inventory.Assignments, inventory.ProjectID, normalizedKind)
	if len(blocking) == 0 {
		return EvaluationResult{Outcome: OutcomeAllowed}
	}

	violations := make([]Violation, 0, len(blocking))
	for _, assignment := range blocking {
		displayName := AssignmentDisplayName(assignment)
		violations = append(violations, Violation{
			Target:       TargetAgentKind,
			TargetValue:  normalizedKind,
			AssignmentID: assignment.AssignmentID,
			DisplayName:  displayName,
			PortalURL:    BuildPolicyAssignmentPortalURL(inventory.ProjectID, assignment.AssignmentID),
			Message: fmt.Sprintf(
				"The %q policy doesn't allow %s agent creation in this project.",
				displayName,
				normalizedKind,
			),
		})
	}
	return EvaluationResult{
		Outcome:    OutcomeBlocked,
		Violations: violations,
	}
}
