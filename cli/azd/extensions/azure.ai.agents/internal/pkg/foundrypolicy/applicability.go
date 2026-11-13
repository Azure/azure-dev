// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore donotenforce policyeffect notScopes

package foundrypolicy

import "github.com/azure/azure-dev/cli/shared/foundry/policy"

// NormalizeScope trims trailing slashes and lowercases an ARM scope string for
// case-insensitive comparison.
func NormalizeScope(scope string) string {
	return policy.NormalizeScope(scope)
}

// IsScopeApplicable reports whether an assignment or exemption scope covers projectID,
// either by direct ARM resource hierarchy prefix or via an ancestor management group.
func IsScopeApplicable(scope, projectID string, managementGroupScopes []string) bool {
	return policy.IsScopeApplicable(scope, projectID, managementGroupScopes)
}

// HasPolicyEffectOverride reports whether the assignment contains a policyEffect override.
func HasPolicyEffectOverride(assignment PolicyAssignment) bool {
	return policy.HasPolicyEffectOverride(assignment)
}

// HasResourceSelectors reports whether the assignment defines resourceSelectors.
func HasResourceSelectors(assignment PolicyAssignment) bool {
	return policy.HasResourceSelectors(assignment)
}

// GetEffectiveEffect resolves the lowercase effective policy effect ("deny", "audit",
// "disabled", etc.) from the assignment's `effect` parameter, falling back to the
// policy definition's defaultValue and policyRule.then.effect.
func GetEffectiveEffect(assignment PolicyAssignment) string {
	return policy.GetEffectiveEffect(assignment)
}

// IsPolicyAssignmentApplicable reports whether a policy assignment actively enforces
// a `Deny` effect on projectID without exemptions, notScopes exclusions, or complex
// overrides/selectors.
func IsPolicyAssignmentApplicable(assignment PolicyAssignment, projectID string) bool {
	return policy.IsPolicyAssignmentApplicable(assignment, projectID)
}

// EffectiveStringSliceParam resolves an array-of-strings parameter from the assignment
// parameters or policy definition defaultValue.
func EffectiveStringSliceParam(assignment PolicyAssignment, paramName string) ([]string, bool) {
	return policy.EffectiveStringSliceParam(assignment, paramName)
}

// MatchesPolicyDefinition reports whether assignment matches a policy definition ID
// or display-name suffix.
func MatchesPolicyDefinition(assignment PolicyAssignment, spec PolicyDefinitionSpec) bool {
	return policy.MatchesPolicyDefinition(assignment, spec)
}

// MatchesAzurePolicyLike implements Azure Policy `like` wildcard matching.
func MatchesAzurePolicyLike(value, pattern string) bool {
	return policy.MatchesAzurePolicyLike(value, pattern)
}

// AssignmentDisplayName returns the human-readable display name for a policy assignment.
func AssignmentDisplayName(assignment PolicyAssignment) string {
	return policy.AssignmentDisplayName(assignment)
}

// SortAssignmentsByID returns a copy of assignments sorted deterministically by AssignmentID.
func SortAssignmentsByID(assignments []PolicyAssignment) []PolicyAssignment {
	return policy.SortAssignmentsByID(assignments)
}
