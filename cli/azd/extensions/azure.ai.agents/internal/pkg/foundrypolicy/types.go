// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package foundrypolicy provides best-effort, fail-open Azure Policy preflight
// evaluation for Microsoft Foundry agents, container images/registries, models,
// and tools by delegating to github.com/azure/azure-dev/cli/shared/foundry/policy.
package foundrypolicy

import "github.com/azure/azure-dev/cli/shared/foundry/policy"

// TargetKind identifies which aspect of an agent configuration was evaluated.
type TargetKind = policy.TargetKind

const (
	TargetAgentKind         = policy.TargetAgentKind
	TargetContainerImage    = policy.TargetContainerImage
	TargetContainerRegistry = policy.TargetContainerRegistry
	TargetModel             = policy.TargetModel
	TargetTool              = policy.TargetTool
)

// EvaluationOutcome represents the policy preflight decision for a candidate.
type EvaluationOutcome = policy.EvaluationOutcome

const (
	OutcomeAllowed       = policy.OutcomeAllowed
	OutcomeBlocked       = policy.OutcomeBlocked
	OutcomeIndeterminate = policy.OutcomeIndeterminate
	OutcomeNotApplicable = policy.OutcomeNotApplicable
)

// Freshness indicates whether a cached policy inventory is within its TTL.
type Freshness = policy.Freshness

const (
	FreshnessFresh = policy.FreshnessFresh
	FreshnessStale = policy.FreshnessStale
)

// ModelCandidate describes a model or model deployment used by an agent.
type ModelCandidate = policy.ModelCandidate

// ToolCandidate describes a tool configured on an agent.
type ToolCandidate = policy.ToolCandidate

// Candidate aggregates all policy-relevant attributes of an agent service at a
// given lifecycle stage (init, predeploy, deploy, or doctor).
type Candidate = policy.Candidate

// Violation describes a single blocking Azure Policy assignment.
type Violation = policy.Violation

// EvaluationResult holds the aggregated outcome of evaluating one or more policy targets.
type EvaluationResult = policy.EvaluationResult

// PolicyAssignment represents a projected row from Azure Resource Graph joining
// microsoft.authorization/policyassignments with microsoft.authorization/policydefinitions.
type PolicyAssignment = policy.PolicyAssignment

// Inventory holds the applicable policy assignments resolved for a Foundry project.
type Inventory = policy.Inventory

// InventoryResult pairs an Inventory with its cache freshness.
type InventoryResult = policy.InventoryResult

// PolicyDefinitionSpec identifies a built-in (or custom suffix-matched) Azure Policy
// definition and its parameter name.
type PolicyDefinitionSpec = policy.PolicyDefinitionSpec

// ModelPolicyEvaluation holds the outcome of evaluating model deployments via ARM.
type ModelPolicyEvaluation = policy.ModelPolicyEvaluation

// DeploymentPolicyEvaluator evaluates ARM deployment policies for model deployments on
// a Foundry Cognitive Services account.
type DeploymentPolicyEvaluator = policy.DeploymentPolicyEvaluator
