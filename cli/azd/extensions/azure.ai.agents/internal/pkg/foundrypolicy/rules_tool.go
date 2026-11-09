// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundrypolicy

import (
	"fmt"
	"slices"
	"strings"
)

const (
	// DeniedToolsDisplaySuffix is the display-name suffix for denied Foundry agent tools.
	DeniedToolsDisplaySuffix = "microsoft foundry agents should not use denied tools"
	// AllowedToolsDisplaySuffix is the display-name suffix for allowed Foundry agent tools.
	AllowedToolsDisplaySuffix = "microsoft foundry agents should only use allowed tools"
)

var (
	deniedToolsSpec = PolicyDefinitionSpec{
		DisplayNameSuffix: DeniedToolsDisplaySuffix,
		ParameterName:     "deniedTools",
		IsDenyList:        true,
	}
	allowedToolsSpec = PolicyDefinitionSpec{
		DisplayNameSuffix: AllowedToolsDisplaySuffix,
		ParameterName:     "allowedTools",
		IsDenyList:        false,
	}
)

// ToolIdentifiers returns the normalized policy identifiers for a ToolCandidate
// (e.g. tool type "code_interpreter", "builtin:code_interpreter", and optional name/connection).
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

// IsToolBlocked reports whether assignment blocks the given tool candidate on projectID.
func IsToolBlocked(assignment PolicyAssignment, projectID string, tool ToolCandidate) bool {
	if !IsPolicyAssignmentApplicable(assignment, projectID) {
		return false
	}
	ids := ToolIdentifiers(tool)
	if len(ids) == 0 {
		return false
	}

	if MatchesPolicyDefinition(assignment, deniedToolsSpec) {
		patterns, ok := EffectiveStringSliceParam(assignment, deniedToolsSpec.ParameterName)
		if !ok {
			return false
		}
		return slices.ContainsFunc(patterns, func(pattern string) bool {
			return slices.ContainsFunc(ids, func(id string) bool {
				return MatchesAzurePolicyLike(id, pattern)
			})
		})
	}

	if MatchesPolicyDefinition(assignment, allowedToolsSpec) {
		patterns, ok := EffectiveStringSliceParam(assignment, allowedToolsSpec.ParameterName)
		if !ok {
			return false
		}
		matched := slices.ContainsFunc(patterns, func(pattern string) bool {
			return slices.ContainsFunc(ids, func(id string) bool {
				return MatchesAzurePolicyLike(id, pattern)
			})
		})
		return !matched
	}

	return false
}

// EvaluateToolPolicy evaluates inventory against all tools configured on an agent.
func EvaluateToolPolicy(inventory Inventory, tools []ToolCandidate) EvaluationResult {
	if len(tools) == 0 {
		return EvaluationResult{Outcome: OutcomeAllowed}
	}

	sorted := SortAssignmentsByID(inventory.Assignments)
	var violations []Violation
	for _, tool := range tools {
		label := strings.TrimSpace(tool.Type)
		if tool.Name != "" && !strings.EqualFold(tool.Name, label) {
			label = fmt.Sprintf("%s (%s)", label, strings.TrimSpace(tool.Name))
		}
		for _, assignment := range sorted {
			if IsToolBlocked(assignment, inventory.ProjectID, tool) {
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
			}
		}
	}

	if len(violations) > 0 {
		return EvaluationResult{
			Outcome:    OutcomeBlocked,
			Violations: violations,
		}
	}
	return EvaluationResult{Outcome: OutcomeAllowed}
}
