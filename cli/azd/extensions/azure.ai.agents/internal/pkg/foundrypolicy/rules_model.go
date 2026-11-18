// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundrypolicy

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/shared/foundry/policy"
)

const (
	// AllowedModelsPolicyID is the built-in Azure Policy definition ID for approved Foundry model deployments.
	AllowedModelsPolicyID = policy.AllowedModelsPolicyID
	// DeniedModelsPolicyID is the built-in Azure Policy definition ID for denied Foundry model deployments.
	DeniedModelsPolicyID = policy.DeniedModelsPolicyID

	// DeniedModelsDisplaySuffix is the display-name suffix for denied Foundry models in Resource Graph.
	DeniedModelsDisplaySuffix = policy.DeniedModelsDisplaySuffix
	// AllowedModelsDisplaySuffix is the display-name suffix for approved Foundry model deployments in Resource Graph.
	AllowedModelsDisplaySuffix = policy.AllowedModelsDisplaySuffix
	// LegacyAllowedModelsDisplaySuffix is the legacy display-name suffix for allowed Foundry models in Resource Graph.
	LegacyAllowedModelsDisplaySuffix = policy.LegacyAllowedModelsDisplaySuffix
)

// MergeModelCandidates deduplicates and merges model candidates that share the same
// normalized deployment name (or model name when deployment name is empty).
func MergeModelCandidates(models []ModelCandidate) []ModelCandidate {
	return policy.MergeModelCandidates(models)
}

// EvaluateModelInventoryAssignment evaluates a single Resource Graph policy assignment against model.
func EvaluateModelInventoryAssignment(
	assignment PolicyAssignment,
	projectID string,
	model ModelCandidate,
) EvaluationOutcome {
	return policy.EvaluateModelInventoryAssignment(assignment, projectID, model)
}

// IsModelBlockedByInventory reports whether a Resource Graph policy assignment blocks model.
func IsModelBlockedByInventory(assignment PolicyAssignment, projectID string, model ModelCandidate) bool {
	return policy.IsModelBlockedByInventory(assignment, projectID, model)
}

// EvaluateModelInventoryPolicy evaluates Resource-Graph-backed model policy assignments.
func EvaluateModelInventoryPolicy(inventory Inventory, models []ModelCandidate) EvaluationResult {
	return policy.EvaluateModelInventoryPolicy(inventory, models)
}

// EvaluateInventoryModelPolicy evaluates Resource-Graph-backed model policy assignments.
func EvaluateInventoryModelPolicy(inventory Inventory, models []ModelCandidate) []Violation {
	return policy.EvaluateInventoryModelPolicy(inventory, models)
}

// ARMDeploymentPolicyEvaluator evaluates model deployment policies using the Cognitive Services
// `evaluateDeploymentPolicies` ARM endpoint.
type ARMDeploymentPolicyEvaluator = policy.ARMDeploymentPolicyEvaluator

// NewARMDeploymentPolicyEvaluator creates an ARMDeploymentPolicyEvaluator backed by pipeline.
func NewARMDeploymentPolicyEvaluator(pipeline runtime.Pipeline, armEndpoint string) *ARMDeploymentPolicyEvaluator {
	return policy.NewARMDeploymentPolicyEvaluator(pipeline, armEndpoint)
}
