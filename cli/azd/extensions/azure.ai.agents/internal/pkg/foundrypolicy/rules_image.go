// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundrypolicy

import "github.com/azure/azure-dev/cli/shared/foundry/policy"

const (
	// DeniedContainerImagesPolicyID is the built-in Azure Policy definition ID for denied hosted container images.
	DeniedContainerImagesPolicyID = policy.DeniedContainerImagesPolicyID
	// AllowedContainerImagesPolicyID is the built-in Azure Policy definition ID for allowed hosted container images.
	AllowedContainerImagesPolicyID = policy.AllowedContainerImagesPolicyID

	// DeniedContainerImagesDisplaySuffix is the display-name suffix for denied hosted container images.
	DeniedContainerImagesDisplaySuffix = policy.DeniedContainerImagesDisplaySuffix
	// AllowedContainerImagesDisplaySuffix is the display-name suffix for allowed hosted container images.
	AllowedContainerImagesDisplaySuffix = policy.AllowedContainerImagesDisplaySuffix
)

// ContainerImageCandidateType distinguishes a full container image reference from a
// registry-only login server check (used before a Dockerfile build produces a repo/tag).
type ContainerImageCandidateType = policy.ContainerImageCandidateType

const (
	CandidateTypeImage    = policy.CandidateTypeImage
	CandidateTypeRegistry = policy.CandidateTypeRegistry
)

// ContainerImageCandidate represents a container image or registry login server to evaluate.
type ContainerImageCandidate = policy.ContainerImageCandidate

// GetRegistryWidePatternRegistry extracts the lowercase registry host when pattern is
// strictly `<registry>/*`.
func GetRegistryWidePatternRegistry(pattern string) (string, bool) {
	return policy.GetRegistryWidePatternRegistry(pattern)
}

// EvaluateContainerImagePolicyAssignment evaluates a single policy assignment against a
// container image or registry candidate.
func EvaluateContainerImagePolicyAssignment(
	assignment PolicyAssignment,
	projectID string,
	candidate ContainerImageCandidate,
) EvaluationOutcome {
	return policy.EvaluateContainerImagePolicyAssignment(assignment, projectID, candidate)
}

// FindBlockingContainerImagePolicies returns all assignments that block candidate on projectID,
// ordered deterministically by assignment ID.
func FindBlockingContainerImagePolicies(
	assignments []PolicyAssignment,
	projectID string,
	candidate ContainerImageCandidate,
) []PolicyAssignment {
	return policy.FindBlockingContainerImagePolicies(assignments, projectID, candidate)
}

// EvaluateContainerImagePolicy evaluates inventory against a container image or registry candidate.
func EvaluateContainerImagePolicy(
	inventory Inventory,
	candidate ContainerImageCandidate,
) EvaluationResult {
	return policy.EvaluateContainerImagePolicy(inventory, candidate)
}
