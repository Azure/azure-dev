// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore donotenforce policyeffect notScopes

package foundrypolicy

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// NormalizeScope trims trailing slashes and lowercases an ARM scope string for
// case-insensitive comparison.
func NormalizeScope(scope string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(scope), "/"))
}

// IsScopeApplicable reports whether an assignment or exemption scope covers projectID,
// either by direct ARM resource hierarchy prefix or via an ancestor management group.
func IsScopeApplicable(scope, projectID string, managementGroupScopes []string) bool {
	normalizedScope := NormalizeScope(scope)
	normalizedProjectID := NormalizeScope(projectID)
	if normalizedScope == "" || normalizedProjectID == "" {
		return false
	}
	if normalizedProjectID == normalizedScope ||
		strings.HasPrefix(normalizedProjectID, normalizedScope+"/") {
		return true
	}
	for _, mgScope := range managementGroupScopes {
		if NormalizeScope(mgScope) == normalizedScope {
			return true
		}
	}
	return false
}

func getAssignmentParameter(parameters map[string]any, name string) any {
	if parameters == nil {
		return nil
	}
	raw, ok := parameters[name]
	if !ok {
		return nil
	}
	paramMap, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return paramMap["value"]
}

func getDefinitionDefault(parameters map[string]any, name string) any {
	if parameters == nil {
		return nil
	}
	raw, ok := parameters[name]
	if !ok {
		return nil
	}
	paramMap, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return paramMap["defaultValue"]
}

// HasPolicyEffectOverride reports whether the assignment contains a policyEffect override.
// Overrides can include selectors that client-side preflight cannot evaluate reliably, so
// they are deferred to authoritative Foundry service enforcement.
func HasPolicyEffectOverride(assignment PolicyAssignment) bool {
	for _, item := range assignment.AssignmentOverrides {
		overrideMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if kind, ok := overrideMap["kind"].(string); ok &&
			strings.EqualFold(strings.TrimSpace(kind), "policyeffect") {
			return true
		}
	}
	return false
}

// HasResourceSelectors reports whether the assignment defines resourceSelectors.
// Resource selectors can scope an assignment by location or resource type in ways
// preflight cannot evaluate reliably, so such assignments fail open.
func HasResourceSelectors(assignment PolicyAssignment) bool {
	return len(assignment.ResourceSelectors) > 0
}

// GetEffectiveEffect resolves the lowercase effective policy effect ("deny", "audit",
// "disabled", etc.) from the assignment's `effect` parameter, falling back to the
// policy definition's defaultValue and policyRule.then.effect.
func GetEffectiveEffect(assignment PolicyAssignment) string {
	val := getAssignmentParameter(assignment.AssignmentParameters, "effect")
	if val == nil {
		val = getDefinitionDefault(assignment.DefinitionParameters, "effect")
	}
	if val == nil {
		val = assignment.DefinitionEffect
	}
	effectStr, ok := val.(string)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(effectStr))
}

// IsPolicyAssignmentApplicable reports whether a policy assignment actively enforces
// a `Deny` effect on projectID without exemptions, notScopes exclusions, or complex
// overrides/selectors.
func IsPolicyAssignmentApplicable(assignment PolicyAssignment, projectID string) bool {
	if strings.EqualFold(strings.TrimSpace(assignment.EnforcementMode), "donotenforce") ||
		assignment.IsExempt ||
		HasPolicyEffectOverride(assignment) ||
		HasResourceSelectors(assignment) ||
		GetEffectiveEffect(assignment) != "deny" {
		return false
	}

	mgScopes := assignment.ManagementGroupScopes
	if assignment.AssignmentScope != "" &&
		!IsScopeApplicable(assignment.AssignmentScope, projectID, mgScopes) {
		return false
	}
	for _, notScope := range assignment.NotScopes {
		if IsScopeApplicable(notScope, projectID, mgScopes) {
			return false
		}
	}
	return true
}

// EffectiveStringSliceParam resolves an array-of-strings parameter from the assignment
// parameters or policy definition defaultValue.
func EffectiveStringSliceParam(assignment PolicyAssignment, paramName string) ([]string, bool) {
	val := getAssignmentParameter(assignment.AssignmentParameters, paramName)
	if val == nil {
		val = getDefinitionDefault(assignment.DefinitionParameters, paramName)
	}
	if val == nil {
		return nil, false
	}
	switch typed := val.(type) {
	case []string:
		return slices.Clone(typed), true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

// MatchesPolicyDefinition reports whether assignment matches a policy definition ID
// or display-name suffix.
func MatchesPolicyDefinition(assignment PolicyAssignment, spec PolicyDefinitionSpec) bool {
	if assignment.PolicyDefinitionID != "" && spec.DefinitionID != "" &&
		NormalizeScope(assignment.PolicyDefinitionID) == NormalizeScope(spec.DefinitionID) {
		return true
	}
	if assignment.DefinitionDisplayName != "" && spec.DisplayNameSuffix != "" {
		displayLower := strings.ToLower(strings.TrimSpace(assignment.DefinitionDisplayName))
		suffixLower := strings.ToLower(strings.TrimSpace(spec.DisplayNameSuffix))
		if strings.HasSuffix(displayLower, suffixLower) {
			return true
		}
	}
	return false
}

// MatchesAzurePolicyLike implements Azure Policy `like` wildcard matching.
// Matching is case-insensitive and `*` is the only wildcard; all other characters
// (including `?` and `#`) are matched literally.
func MatchesAzurePolicyLike(value, pattern string) bool {
	var expr strings.Builder
	expr.WriteString("(?i)^")
	for _, ch := range strings.TrimSpace(pattern) {
		if ch == '*' {
			expr.WriteString(".*")
		} else {
			expr.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	expr.WriteString("$")

	re, err := regexp.Compile(expr.String())
	if err != nil {
		return false
	}
	return re.MatchString(strings.TrimSpace(value))
}

// AssignmentDisplayName returns the human-readable display name for a policy assignment.
func AssignmentDisplayName(assignment PolicyAssignment) string {
	if s := strings.TrimSpace(assignment.AssignmentDisplayName); s != "" {
		return s
	}
	if s := strings.TrimSpace(assignment.AssignmentName); s != "" {
		return s
	}
	if s := strings.TrimSpace(assignment.DefinitionDisplayName); s != "" {
		return s
	}
	return "Azure Policy assignment"
}

// SortAssignmentsByID returns a copy of assignments sorted deterministically by AssignmentID.
func SortAssignmentsByID(assignments []PolicyAssignment) []PolicyAssignment {
	sorted := slices.Clone(assignments)
	slices.SortFunc(sorted, func(a, b PolicyAssignment) int {
		return cmp.Compare(a.AssignmentID, b.AssignmentID)
	})
	return sorted
}
