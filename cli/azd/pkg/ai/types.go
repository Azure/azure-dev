// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

// AiModel represents an AI model available in the Azure Cognitive Services catalog.
// It is SDK-agnostic and decoupled from armcognitiveservices types.
type AiModel struct {
	// Name is the model name, e.g. "gpt-4o".
	Name string
	// Format is the model format, e.g. "OpenAI".
	Format string
	// LifecycleStatus is the model lifecycle status, e.g. "preview", "stable".
	LifecycleStatus string
	// Capabilities lists the model's capabilities, e.g. ["chat", "embeddings"].
	Capabilities []string
	// Versions lists the available versions of this model.
	Versions []AiModelVersion
	// Locations lists the Azure locations where this model is available.
	Locations []string
}

// AiModelVersion represents a specific version of an AI model.
type AiModelVersion struct {
	// Version is the version string, e.g. "2024-05-13".
	Version string
	// IsDefault indicates whether this is the default version.
	IsDefault bool
	// Skus lists the available SKUs for this version.
	Skus []AiModelSku
}

// AiModelSku represents a deployment SKU with its capacity constraints.
type AiModelSku struct {
	// Name is the SKU name, e.g. "GlobalStandard", "Standard".
	Name string
	// UsageName is the quota usage name used to join with usage/quota data,
	// e.g. "OpenAI.Standard.gpt-4o".
	UsageName string
	// DefaultCapacity is the suggested deployment capacity (0 if unavailable).
	DefaultCapacity int32
	// MinCapacity is the minimum allowed deployment capacity.
	MinCapacity int32
	// MaxCapacity is the maximum allowed deployment capacity.
	MaxCapacity int32
	// CapacityStep is the capacity increment granularity.
	CapacityStep int32
}

// AiModelDeployment is a fully resolved deployment configuration.
//
// Capacity vs Quota:
//   - Capacity is deployment-level: how many units this specific deployment will consume.
//   - RemainingQuota is subscription-level: how much total capacity remains at this
//     location for this SKU across all deployments (limit - current_value from usage API).
//
// Constraint: Capacity must be <= RemainingQuota for the deployment to succeed.
type AiModelDeployment struct {
	// ModelName is the model name, e.g. "gpt-4o".
	ModelName string
	// Format is the model format, e.g. "OpenAI".
	Format string
	// Version is the model version, e.g. "2024-05-13".
	Version string
	// Location is the Azure location for this deployment.
	Location string
	// Sku is the selected SKU for this deployment.
	Sku AiModelSku
	// Capacity is the resolved deployment capacity in units.
	// Resolved from: DeploymentOptions.Capacity → Sku.DefaultCapacity → 0 (caller must handle).
	Capacity int32
	// RemainingQuota is the subscription quota remaining at this location for this SKU.
	// Only populated when a quota check is performed. nil means no quota check was done.
	RemainingQuota *float64
}

// AiModelUsage represents a subscription-level quota/usage entry for a specific
// model SKU at a location.
type AiModelUsage struct {
	// Name is the quota usage name, e.g. "OpenAI.Standard.gpt-4o".
	Name string
	// CurrentValue is the amount of quota currently consumed.
	CurrentValue float64
	// Limit is the total quota limit for this usage name.
	Limit float64
}

// ModelLocationQuota represents model quota availability in a specific location.
type ModelLocationQuota struct {
	// Location is the Azure location name.
	Location string
	// MaxRemainingQuota is the maximum remaining quota across model SKUs with usage entries.
	MaxRemainingQuota float64
}

// QuotaRequirement specifies a single quota check: the usage name to check
// and the minimum remaining capacity needed.
type QuotaRequirement struct {
	// UsageName is the quota usage name to check, e.g. "OpenAI.Standard.gpt-4o".
	UsageName string
	// MinCapacity is the minimum remaining capacity needed. If 0, defaults to 1.
	MinCapacity float64
}

// QuotaCheckOptions enables quota-aware model/deployment selection.
// When provided, the service fetches usage data alongside the model catalog
// and cross-references via AiModelSku.UsageName == AiModelUsage.Name.
type QuotaCheckOptions struct {
	// MinRemainingCapacity is the minimum remaining quota required per SKU.
	// Models/deployments where no SKU meets this threshold are excluded.
	// 0 means "any remaining > 0" (i.e. not fully exhausted).
	MinRemainingCapacity float64
}

// FilterOptions specifies criteria for filtering AI models.
type FilterOptions struct {
	// Locations filters to models available at these locations.
	Locations []string
	// Capabilities filters by model capabilities, e.g. ["chat", "embeddings"].
	Capabilities []string
	// Formats filters by model format, e.g. ["OpenAI"].
	Formats []string
	// Statuses filters by lifecycle status, e.g. ["preview", "stable"].
	Statuses []string
	// ExcludeModelNames excludes models by name (for multi-model selection flows).
	ExcludeModelNames []string
}

// DeploymentOptions specifies preferences for resolving a model deployment.
// All fields are optional filters. When empty, no filtering is applied for that dimension.
type DeploymentOptions struct {
	// Locations lists preferred locations. If empty, location is left unset on results.
	Locations []string
	// Versions lists preferred versions. If empty, all versions are included.
	Versions []string
	// Skus lists preferred SKU names, e.g. ["GlobalStandard", "Standard"]. If empty, all SKUs are included.
	Skus []string
	// Capacity is the preferred deployment capacity. If set and valid
	// (within min/max, aligned to step), used directly. If nil, uses SKU default.
	Capacity *int32
}
