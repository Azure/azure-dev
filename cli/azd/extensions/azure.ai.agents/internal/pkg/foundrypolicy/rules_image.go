// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundrypolicy

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	// DeniedContainerImagesPolicyID is the built-in Azure Policy definition ID for denied hosted container images.
	DeniedContainerImagesPolicyID = "/providers/microsoft.authorization/policydefinitions/" +
		"215c2c77-3afb-4f55-be2f-709b60dc8d53"
	// AllowedContainerImagesPolicyID is the built-in Azure Policy definition ID for allowed hosted container images.
	AllowedContainerImagesPolicyID = "/providers/microsoft.authorization/policydefinitions/" +
		"d0d9ac6c-628b-46c1-a177-eb22b12ce588"

	// DeniedContainerImagesDisplaySuffix is the display-name suffix for denied hosted container images.
	DeniedContainerImagesDisplaySuffix = "microsoft foundry hosted agents should not use denied container images"
	// AllowedContainerImagesDisplaySuffix is the display-name suffix for allowed hosted container images.
	AllowedContainerImagesDisplaySuffix = "microsoft foundry hosted agents should only use allowed container images"
)

var (
	deniedContainerImagesSpec = PolicyDefinitionSpec{
		DefinitionID:      DeniedContainerImagesPolicyID,
		DisplayNameSuffix: DeniedContainerImagesDisplaySuffix,
		ParameterName:     "deniedContainerImages",
		IsDenyList:        true,
	}
	allowedContainerImagesSpec = PolicyDefinitionSpec{
		DefinitionID:      AllowedContainerImagesPolicyID,
		DisplayNameSuffix: AllowedContainerImagesDisplaySuffix,
		ParameterName:     "allowedContainerImages",
		IsDenyList:        false,
	}
)

var registryInvalidCharsRe = regexp.MustCompile(`[/\\*?#]`)

// ContainerImageCandidateType distinguishes a full container image reference from a
// registry-only login server check (used before a Dockerfile build produces a repo/tag).
type ContainerImageCandidateType string

const (
	CandidateTypeImage    ContainerImageCandidateType = "image"
	CandidateTypeRegistry ContainerImageCandidateType = "registry"
)

// ContainerImageCandidate represents a container image or registry login server to evaluate.
type ContainerImageCandidate struct {
	Type  ContainerImageCandidateType
	Value string
}

func normalizeRegistryCandidate(value string) string {
	s := strings.TrimSpace(value)
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
			s = s[len(prefix):]
			break
		}
	}
	return strings.ToLower(strings.TrimRight(s, "/"))
}

// GetRegistryWidePatternRegistry extracts the lowercase registry host when pattern is
// strictly `<registry>/*`. Narrower repository or tag patterns return ("", false) because
// registry-only preflight cannot know the future repository or tag.
func GetRegistryWidePatternRegistry(pattern string) (string, bool) {
	trimmed := strings.TrimSpace(pattern)
	if !strings.HasSuffix(trimmed, "/*") {
		return "", false
	}
	registry := trimmed[:len(trimmed)-2]
	if registry == "" || registryInvalidCharsRe.MatchString(registry) {
		return "", false
	}
	return strings.ToLower(registry), true
}

// EvaluateContainerImagePolicyAssignment evaluates a single policy assignment against a
// container image or registry candidate.
func EvaluateContainerImagePolicyAssignment(
	assignment PolicyAssignment,
	projectID string,
	candidate ContainerImageCandidate,
) EvaluationOutcome {
	if !IsPolicyAssignmentApplicable(assignment, projectID) {
		return OutcomeNotApplicable
	}

	isDenied := MatchesPolicyDefinition(assignment, deniedContainerImagesSpec)
	isAllowed := MatchesPolicyDefinition(assignment, allowedContainerImagesSpec)
	if !isDenied && !isAllowed {
		return OutcomeNotApplicable
	}

	paramName := allowedContainerImagesSpec.ParameterName
	if isDenied {
		paramName = deniedContainerImagesSpec.ParameterName
	}
	patterns, ok := EffectiveStringSliceParam(assignment, paramName)
	if !ok {
		return OutcomeNotApplicable
	}

	if candidate.Type == CandidateTypeImage {
		matched := slices.ContainsFunc(patterns, func(pattern string) bool {
			return MatchesAzurePolicyLike(candidate.Value, pattern)
		})
		if isDenied {
			if matched {
				return OutcomeBlocked
			}
			return OutcomeAllowed
		}
		if matched {
			return OutcomeAllowed
		}
		return OutcomeBlocked
	}

	registry := normalizeRegistryCandidate(candidate.Value)
	registryWidePatterns := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if reg, ok := GetRegistryWidePatternRegistry(p); ok {
			registryWidePatterns = append(registryWidePatterns, reg)
		}
	}

	if isDenied {
		if slices.Contains(registryWidePatterns, registry) {
			return OutcomeBlocked
		}
		if len(registryWidePatterns) == len(patterns) {
			return OutcomeAllowed
		}
		return OutcomeIndeterminate
	}

	if slices.Contains(registryWidePatterns, registry) {
		return OutcomeAllowed
	}
	if len(registryWidePatterns) == len(patterns) {
		return OutcomeBlocked
	}
	return OutcomeIndeterminate
}

// FindBlockingContainerImagePolicies returns all assignments that block candidate on projectID,
// ordered deterministically by assignment ID.
func FindBlockingContainerImagePolicies(
	assignments []PolicyAssignment,
	projectID string,
	candidate ContainerImageCandidate,
) []PolicyAssignment {
	sorted := SortAssignmentsByID(assignments)
	var blocking []PolicyAssignment
	for _, assignment := range sorted {
		if EvaluateContainerImagePolicyAssignment(assignment, projectID, candidate) == OutcomeBlocked {
			blocking = append(blocking, assignment)
		}
	}
	return blocking
}

// EvaluateContainerImagePolicy evaluates inventory against a container image or registry candidate.
func EvaluateContainerImagePolicy(
	inventory Inventory,
	candidate ContainerImageCandidate,
) EvaluationResult {
	blocking := FindBlockingContainerImagePolicies(inventory.Assignments, inventory.ProjectID, candidate)
	if len(blocking) > 0 {
		target := TargetContainerImage
		noun := "container image"
		if candidate.Type == CandidateTypeRegistry {
			target = TargetContainerRegistry
			noun = "container registry"
		}
		violations := make([]Violation, 0, len(blocking))
		for _, assignment := range blocking {
			displayName := AssignmentDisplayName(assignment)
			violations = append(violations, Violation{
				Target:       target,
				TargetValue:  strings.TrimSpace(candidate.Value),
				AssignmentID: assignment.AssignmentID,
				DisplayName:  displayName,
				PortalURL:    BuildPolicyAssignmentPortalURL(inventory.ProjectID, assignment.AssignmentID),
				Message: fmt.Sprintf(
					"The %q policy doesn't allow this %s (%s).",
					displayName,
					noun,
					strings.TrimSpace(candidate.Value),
				),
			})
		}
		return EvaluationResult{
			Outcome:    OutcomeBlocked,
			Violations: violations,
		}
	}

	for _, assignment := range inventory.Assignments {
		if EvaluateContainerImagePolicyAssignment(assignment, inventory.ProjectID, candidate) == OutcomeIndeterminate {
			return EvaluationResult{Outcome: OutcomeIndeterminate}
		}
	}
	return EvaluationResult{Outcome: OutcomeAllowed}
}
