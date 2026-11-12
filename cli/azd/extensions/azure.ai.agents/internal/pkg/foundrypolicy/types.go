// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package foundrypolicy provides best-effort, fail-open Azure Policy preflight
// evaluation for Microsoft Foundry agents, container images/registries, models,
// and tools.
package foundrypolicy

import "context"

// TargetKind identifies which aspect of an agent configuration was evaluated.
type TargetKind string

const (
	TargetAgentKind         TargetKind = "agent_kind"
	TargetContainerImage    TargetKind = "container_image"
	TargetContainerRegistry TargetKind = "container_registry"
	TargetModel             TargetKind = "model"
	TargetTool              TargetKind = "tool"
)

// EvaluationOutcome represents the policy preflight decision for a candidate.
type EvaluationOutcome string

const (
	OutcomeAllowed       EvaluationOutcome = "allowed"
	OutcomeBlocked       EvaluationOutcome = "blocked"
	OutcomeIndeterminate EvaluationOutcome = "indeterminate"
	OutcomeNotApplicable EvaluationOutcome = "notApplicable"
)

// Freshness indicates whether a cached policy inventory is within its TTL.
type Freshness string

const (
	FreshnessFresh Freshness = "fresh"
	FreshnessStale Freshness = "stale"
)

// ModelCandidate describes a model or model deployment used by an agent.
type ModelCandidate struct {
	// DeploymentName is the Foundry / Cognitive Services model deployment name.
	DeploymentName string
	// Name is the underlying model name (e.g. "gpt-4.1").
	Name string
	// Format is the model format (e.g. "OpenAI").
	Format string
	// Version is the model version string (e.g. "2025-04-14").
	Version string
	// Publisher is the model publisher when explicitly known (e.g. "OpenAI").
	Publisher string
	// AssetID is the full AzureML model asset ID when explicitly known.
	AssetID string
}

// ToolCandidate describes a tool configured on an agent.
type ToolCandidate struct {
	// Type is the tool type (e.g. "code_interpreter", "web_search", "mcp", "azure_ai_search").
	Type string
	// Name is the optional user-facing or connection/toolbox identifier for the tool.
	Name string
	// ConnectionID is an optional Foundry project connection name or ARM resource ID.
	ConnectionID string
	// ServerURL is an optional MCP server endpoint URL (when Type is "mcp").
	ServerURL string
	// ConnectorID is an optional MCP connector identifier (when Type is "mcp").
	ConnectorID string
}

// Candidate aggregates all policy-relevant attributes of an agent service at a
// given lifecycle stage (init, predeploy, deploy, or doctor). Callers populate
// whichever fields are known at that stage.
type Candidate struct {
	ProjectResourceID string
	ServiceName       string
	AgentKind         string
	ContainerImage    string
	ContainerRegistry string
	Models            []ModelCandidate
	Tools             []ToolCandidate
}

// Violation describes a single blocking Azure Policy assignment.
type Violation struct {
	Target       TargetKind `json:"target"`
	TargetValue  string     `json:"targetValue,omitempty"`
	AssignmentID string     `json:"assignmentId"`
	DisplayName  string     `json:"displayName"`
	PortalURL    string     `json:"portalUrl"`
	Message      string     `json:"message,omitempty"`
}

// EvaluationResult holds the aggregated outcome of evaluating one or more policy targets.
type EvaluationResult struct {
	Outcome          EvaluationOutcome `json:"status"`
	Violations       []Violation       `json:"policies,omitempty"`
	Freshness        Freshness         `json:"freshness,omitempty"`
	IncompleteReason string            `json:"incompleteReason,omitempty"`
}

// PolicyAssignment represents a projected row from Azure Resource Graph joining
// microsoft.authorization/policyassignments with microsoft.authorization/policydefinitions.
type PolicyAssignment struct {
	AssignmentID          string         `json:"assignmentId"`
	AssignmentScope       string         `json:"assignmentScope,omitempty"`
	PolicyDefinitionID    string         `json:"policyDefinitionId,omitempty"`
	AssignmentDisplayName string         `json:"assignmentDisplayName,omitempty"`
	AssignmentName        string         `json:"assignmentName,omitempty"`
	AssignmentParameters  map[string]any `json:"assignmentParameters,omitempty"`
	AssignmentOverrides   []any          `json:"assignmentOverrides,omitempty"`
	ResourceSelectors     []any          `json:"resourceSelectors,omitempty"`
	DefinitionDisplayName string         `json:"definitionDisplayName,omitempty"`
	DefinitionParameters  map[string]any `json:"definitionParameters,omitempty"`
	DefinitionEffect      any            `json:"definitionEffect,omitempty"`
	EnforcementMode       string         `json:"enforcementMode,omitempty"`
	NotScopes             []string       `json:"notScopes,omitempty"`
	ManagementGroupScopes []string       `json:"-"`
	IsExempt              bool           `json:"-"`
}

// Inventory holds the applicable policy assignments resolved for a Foundry project.
type Inventory struct {
	ProjectID   string
	Assignments []PolicyAssignment
}

// InventoryResult pairs an Inventory with its cache freshness.
type InventoryResult struct {
	Inventory Inventory
	Freshness Freshness
}

// PolicyDefinitionSpec identifies a built-in (or custom suffix-matched) Azure Policy
// definition and its parameter name.
type PolicyDefinitionSpec struct {
	DefinitionID      string
	DisplayNameSuffix string
	ParameterName     string
	IsDenyList        bool
}

// ModelPolicyEvaluation holds the outcome of evaluating model deployments via ARM.
type ModelPolicyEvaluation struct {
	Violations       []Violation
	ResolvedModels   []ModelCandidate
	IncompleteReason string
}

// DeploymentPolicyEvaluator evaluates ARM deployment policies for model deployments on
// a Foundry Cognitive Services account.
type DeploymentPolicyEvaluator interface {
	EvaluateModelDeployments(
		ctx context.Context,
		projectResourceID string,
		models []ModelCandidate,
	) (ModelPolicyEvaluation, error)
}
