// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundrypolicy

import (
	"fmt"
	"slices"
	"strings"
)

const (
	// DeniedToolsPolicyID is the built-in Azure Policy definition ID for denied Foundry tools.
	DeniedToolsPolicyID = "/providers/microsoft.authorization/policydefinitions/7740c22b-07ef-4bac-9f5a-e38d188f80b7"
	// AllowedToolsPolicyID is the built-in Azure Policy definition ID for approved Foundry tools.
	AllowedToolsPolicyID = "/providers/microsoft.authorization/policydefinitions/e7352ad2-6c86-4a07-9573-47f67b557ba8"

	// DeniedToolsDisplaySuffix is the display-name suffix for denied Foundry agent and toolbox tools.
	DeniedToolsDisplaySuffix = "microsoft foundry agents and toolboxes should not use denied tools"
	// LegacyDeniedToolsDisplaySuffix is the legacy display-name suffix for denied Foundry agent tools.
	LegacyDeniedToolsDisplaySuffix = "microsoft foundry agents should not use denied tools"
	// AllowedToolsDisplaySuffix is the display-name suffix for approved Foundry agent and toolbox tools.
	AllowedToolsDisplaySuffix = "microsoft foundry agents and toolboxes should only use approved tools"
	// LegacyAllowedToolsDisplaySuffix is the legacy display-name suffix for allowed Foundry agent tools.
	LegacyAllowedToolsDisplaySuffix = "microsoft foundry agents should only use allowed tools"
)

var (
	deniedToolsSpec = PolicyDefinitionSpec{
		DefinitionID:      DeniedToolsPolicyID,
		DisplayNameSuffix: DeniedToolsDisplaySuffix,
		ParameterName:     "deniedToolTypes",
		IsDenyList:        true,
	}
	legacyDeniedToolsSpec = PolicyDefinitionSpec{
		DisplayNameSuffix: LegacyDeniedToolsDisplaySuffix,
		ParameterName:     "deniedTools",
		IsDenyList:        true,
	}
	allowedToolsSpec = PolicyDefinitionSpec{
		DefinitionID:      AllowedToolsPolicyID,
		DisplayNameSuffix: AllowedToolsDisplaySuffix,
		ParameterName:     "approvedToolTypes",
		IsDenyList:        false,
	}
	legacyAllowedToolsSpec = PolicyDefinitionSpec{
		DisplayNameSuffix: LegacyAllowedToolsDisplaySuffix,
		ParameterName:     "allowedTools",
		IsDenyList:        false,
	}
)

// ToolIdentifiers returns the normalized policy identifiers for legacy `deniedTools` / `allowedTools`
// matching (e.g. tool type "code_interpreter", "builtin:code_interpreter", and optional name/connection).
func ToolIdentifiers(tool ToolCandidate) []string {
	var ids []string
	add := func(v string) {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" && !slices.Contains(ids, v) {
			ids = append(ids, v)
		}
	}
	toolType := strings.ToLower(strings.TrimSpace(tool.Type))
	if toolType != "" {
		add(toolType)
		add("builtin:" + toolType)
	}
	if name := strings.TrimSpace(tool.Name); name != "" {
		add(name)
	}
	if conn := strings.TrimSpace(tool.ConnectionID); conn != "" {
		add(conn)
		add("connection:" + conn)
	}
	return ids
}

// EvaluateToolPolicyAssignment evaluates a single policy assignment against a tool candidate.
func EvaluateToolPolicyAssignment(
	assignment PolicyAssignment,
	projectID string,
	tool ToolCandidate,
) EvaluationOutcome {
	if !IsPolicyAssignmentApplicable(assignment, projectID) {
		return OutcomeNotApplicable
	}
	toolType := strings.TrimSpace(tool.Type)
	if toolType == "" {
		return OutcomeNotApplicable
	}
	isMCP := strings.EqualFold(toolType, "mcp")
	serverURL := strings.TrimSpace(tool.ServerURL)
	connectorID := strings.TrimSpace(tool.ConnectorID)

	if MatchesPolicyDefinition(assignment, deniedToolsSpec) ||
		MatchesPolicyDefinition(assignment, legacyDeniedToolsSpec) {
		deniedTypes, hasDeniedTypes := EffectiveStringSliceParam(assignment, "deniedToolTypes")
		deniedEndpoints, hasDeniedEndpoints := EffectiveStringSliceParam(assignment, "deniedMcpEndpoints")
		deniedConnectors, hasDeniedConnectors := EffectiveStringSliceParam(assignment, "deniedMcpConnectors")
		isBuiltInDeny := hasDeniedTypes || hasDeniedEndpoints || hasDeniedConnectors ||
			NormalizeScope(assignment.PolicyDefinitionID) == NormalizeScope(DeniedToolsPolicyID)

		if isBuiltInDeny {
			if !hasDeniedTypes && !hasDeniedEndpoints && !hasDeniedConnectors {
				return OutcomeNotApplicable
			}
			if hasDeniedTypes && slices.ContainsFunc(deniedTypes, func(entry string) bool {
				return strings.EqualFold(toolType, strings.TrimSpace(entry))
			}) {
				return OutcomeBlocked
			}
			if isMCP {
				if serverURL != "" && hasDeniedEndpoints &&
					slices.ContainsFunc(deniedEndpoints, func(pattern string) bool {
						return MatchesAzurePolicyLike(serverURL, pattern)
					}) {
					return OutcomeBlocked
				}
				if connectorID != "" && hasDeniedConnectors &&
					slices.ContainsFunc(deniedConnectors, func(entry string) bool {
						return strings.EqualFold(connectorID, strings.TrimSpace(entry))
					}) {
					return OutcomeBlocked
				}
				if (len(deniedEndpoints) > 0 && serverURL == "") ||
					(len(deniedConnectors) > 0 && connectorID == "" && serverURL == "") {
					return OutcomeIndeterminate
				}
			}
			return OutcomeAllowed
		}

		patterns, ok := EffectiveStringSliceParam(assignment, "deniedTools")
		if !ok {
			return OutcomeNotApplicable
		}
		ids := ToolIdentifiers(tool)
		if slices.ContainsFunc(patterns, func(pattern string) bool {
			return slices.ContainsFunc(ids, func(id string) bool {
				return MatchesAzurePolicyLike(id, pattern)
			})
		}) {
			return OutcomeBlocked
		}
		return OutcomeAllowed
	}

	if MatchesPolicyDefinition(assignment, allowedToolsSpec) ||
		MatchesPolicyDefinition(assignment, legacyAllowedToolsSpec) {
		approvedTypes, hasApprovedTypes := EffectiveStringSliceParam(assignment, "approvedToolTypes")
		approvedEndpoints, hasApprovedEndpoints := EffectiveStringSliceParam(assignment, "approvedMcpEndpoints")
		approvedConnectors, hasApprovedConnectors := EffectiveStringSliceParam(assignment, "approvedMcpConnectors")
		isBuiltInApprove := hasApprovedTypes || hasApprovedEndpoints || hasApprovedConnectors ||
			NormalizeScope(assignment.PolicyDefinitionID) == NormalizeScope(AllowedToolsPolicyID)

		if isBuiltInApprove {
			if !hasApprovedTypes && !hasApprovedEndpoints && !hasApprovedConnectors {
				return OutcomeNotApplicable
			}
			if hasApprovedTypes && slices.ContainsFunc(approvedTypes, func(entry string) bool {
				return strings.EqualFold(toolType, strings.TrimSpace(entry))
			}) {
				return OutcomeAllowed
			}
			if isMCP {
				if serverURL != "" && hasApprovedEndpoints &&
					slices.ContainsFunc(approvedEndpoints, func(pattern string) bool {
						return MatchesAzurePolicyLike(serverURL, pattern)
					}) {
					return OutcomeAllowed
				}
				if connectorID != "" && hasApprovedConnectors &&
					slices.ContainsFunc(approvedConnectors, func(entry string) bool {
						return strings.EqualFold(connectorID, strings.TrimSpace(entry))
					}) {
					return OutcomeAllowed
				}
				if (len(approvedEndpoints) > 0 && serverURL == "") ||
					(len(approvedConnectors) > 0 && connectorID == "" && serverURL == "") {
					return OutcomeIndeterminate
				}
			}
			return OutcomeBlocked
		}

		patterns, ok := EffectiveStringSliceParam(assignment, "allowedTools")
		if !ok {
			return OutcomeNotApplicable
		}
		ids := ToolIdentifiers(tool)
		matched := slices.ContainsFunc(patterns, func(pattern string) bool {
			return slices.ContainsFunc(ids, func(id string) bool {
				return MatchesAzurePolicyLike(id, pattern)
			})
		})
		if !matched {
			return OutcomeBlocked
		}
		return OutcomeAllowed
	}

	return OutcomeNotApplicable
}

// IsToolBlocked reports whether assignment blocks the given tool candidate on projectID.
func IsToolBlocked(assignment PolicyAssignment, projectID string, tool ToolCandidate) bool {
	return EvaluateToolPolicyAssignment(assignment, projectID, tool) == OutcomeBlocked
}

// EvaluateToolPolicy evaluates inventory against all tools configured on an agent.
func EvaluateToolPolicy(inventory Inventory, tools []ToolCandidate) EvaluationResult {
	if len(tools) == 0 {
		return EvaluationResult{Outcome: OutcomeAllowed}
	}

	sorted := SortAssignmentsByID(inventory.Assignments)
	var violations []Violation
	hasIndeterminate := false
	for _, tool := range tools {
		label := strings.TrimSpace(tool.Type)
		if tool.Name != "" && !strings.EqualFold(tool.Name, label) {
			label = fmt.Sprintf("%s (%s)", label, strings.TrimSpace(tool.Name))
		}
		for _, assignment := range sorted {
			switch EvaluateToolPolicyAssignment(assignment, inventory.ProjectID, tool) {
			case OutcomeBlocked:
				displayName := AssignmentDisplayName(assignment)
				violations = append(violations, Violation{
					Target:       TargetTool,
					TargetValue:  label,
					AssignmentID: assignment.AssignmentID,
					DisplayName:  displayName,
					PortalURL:    BuildPolicyAssignmentPortalURL(inventory.ProjectID, assignment.AssignmentID),
					Message: fmt.Sprintf(
						"The %q policy doesn't allow tool %q in this project.",
						displayName,
						label,
					),
				})
			case OutcomeIndeterminate:
				hasIndeterminate = true
			}
		}
	}

	if len(violations) > 0 {
		return EvaluationResult{
			Outcome:    OutcomeBlocked,
			Violations: violations,
		}
	}
	if hasIndeterminate {
		return EvaluationResult{
			Outcome:          OutcomeIndeterminate,
			IncompleteReason: "MCP tool endpoint or connector metadata could not be verified locally against tool policy",
		}
	}
	return EvaluationResult{Outcome: OutcomeAllowed}
}
