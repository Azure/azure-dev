// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundrypolicy

import (
	"context"
	"time"

	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/shared/foundry/policy"
)

const (
	DefaultPolicyCacheTTL         = policy.DefaultPolicyCacheTTL
	DefaultPolicyPreflightTimeout = policy.DefaultPolicyPreflightTimeout
)

// ResourceGraphOptions holds query options for Azure Resource Graph requests.
type ResourceGraphOptions = policy.ResourceGraphOptions

// ResourceGraphRequest represents the POST payload sent to Azure Resource Graph.
type ResourceGraphRequest = policy.ResourceGraphRequest

// ResourceGraphQueryFunc executes a Resource Graph query and returns raw JSON rows.
type ResourceGraphQueryFunc = policy.ResourceGraphQueryFunc

// InventoryCache caches policy inventories per normalized Foundry project ID.
type InventoryCache = policy.InventoryCache

// DefaultCache is the shared process-wide policy inventory cache.
var DefaultCache = policy.DefaultCache

// BuildManagementGroupQuery builds the Kusto query for the subscription's management group ancestors.
func BuildManagementGroupQuery(subscriptionID string) string {
	return policy.BuildManagementGroupQuery(subscriptionID)
}

// BuildPolicyAssignmentsQuery builds the Kusto query that joins direct policy assignments
// with the built-in Foundry policy definitions for agent kinds, container images, tools, and models.
func BuildPolicyAssignmentsQuery() string {
	return policy.BuildPolicyAssignmentsQuery()
}

// BuildPolicyExemptionsQuery builds the Kusto query for active policy exemptions
// targeting the given policy assignment IDs.
func BuildPolicyExemptionsQuery(assignmentIDs []string) string {
	return policy.BuildPolicyExemptionsQuery(assignmentIDs)
}

// NewARMPipeline creates a runtime.Pipeline configured for Azure Resource Manager requests.
func NewARMPipeline(cred azcore.TokenCredential) runtime.Pipeline {
	return policy.NewARMPipeline(cred, "azure-ai-agents-policy", "v1.0.0", azure.NewArmClientOptions())
}

// NewResourceGraphQueryFunc creates a ResourceGraphQueryFunc that handles continuation tokens
// and diagnostic logging while returning safe user-facing errors.
func NewResourceGraphQueryFunc(
	pipeline runtime.Pipeline,
	armEndpoint string,
	logError func(string),
) ResourceGraphQueryFunc {
	return policy.NewResourceGraphQueryFunc(pipeline, armEndpoint, logError)
}

// LoadInventory queries Azure Resource Graph for management group ancestors, applicable
// policy assignments, and active exemptions for the given Foundry project.
func LoadInventory(
	ctx context.Context,
	projectID string,
	subscriptionID string,
	queryFn ResourceGraphQueryFunc,
) (Inventory, error) {
	return policy.LoadInventory(ctx, projectID, subscriptionID, queryFn)
}

// VerifyPolicyAssignmentsReadAccess probes ARM policyAssignments read access on the Foundry
// project scope.
func VerifyPolicyAssignmentsReadAccess(
	ctx context.Context,
	pipeline runtime.Pipeline,
	armEndpoint string,
	projectResourceID string,
) error {
	return policy.VerifyPolicyAssignmentsReadAccess(ctx, pipeline, armEndpoint, projectResourceID)
}

// EvaluateCandidate evaluates all populated fields on candidate (agent kind, container image/registry,
// tools, and models) against the loaded inventory and optional ARM deployment policy evaluator.
func EvaluateCandidate(
	ctx context.Context,
	inventory Inventory,
	candidate Candidate,
	modelEvaluator DeploymentPolicyEvaluator,
) EvaluationResult {
	return policy.EvaluateCandidate(ctx, inventory, candidate, modelEvaluator)
}

// NewInventoryCache constructs a new InventoryCache.
func NewInventoryCache(
	ttl time.Duration,
	now func() time.Time,
	logWarning func(string),
	retryThrottle time.Duration,
	requestTimeout time.Duration,
) *InventoryCache {
	return policy.NewInventoryCache(ttl, now, logWarning, retryThrottle, requestTimeout)
}
