// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"encoding/json"
)

// PredictedResource represents a resource from the Bicep snapshot's predicted
// resources list. This mirrors the essential fields of the ARM template resource
// definition that extension authors commonly need for validation checks.
type PredictedResource struct {
	// Type is the resource type including namespace (e.g. "Microsoft.Storage/storageAccounts").
	Type string `json:"type"`
	// APIVersion is the REST API version for the resource (e.g. "2023-01-01").
	APIVersion string `json:"apiVersion"`
	// Name is the resource name, may contain ARM template expressions.
	Name string `json:"name"`
	// Location is the deployment location for the resource.
	Location string `json:"location,omitempty"`
	// Kind is the resource kind (e.g. "StorageV2" for storage or "app,linux" for web apps).
	Kind string `json:"kind,omitempty"`
	// DependsOn lists symbolic names or resource IDs of resources that must be deployed first.
	DependsOn []string `json:"dependsOn,omitempty"`
	// Properties is the resource-specific configuration as raw JSON.
	Properties json.RawMessage `json:"properties,omitempty"`
	// SKU is the pricing tier / SKU block as raw JSON.
	SKU json.RawMessage `json:"sku,omitempty"`
	// Tags holds resource tags as raw JSON (may be a map or ARM expression).
	Tags json.RawMessage `json:"tags,omitempty"`
	// Scope is used for extension or cross-scope resources.
	Scope string `json:"scope,omitempty"`
}

// ValidationContext holds the assembled context data for a validation check.
// It is populated from PrepareValidationContextChunk messages and injected
// into the provider's Validate call by the ValidationManager.
//
// The contents of Data depend on CheckType — use the typed accessors below
// rather than reading Data directly:
//
//   - ValidationCheckTypeArmProvision ("arm-provision"): a Bicep-only,
//     ARM-rich context. Accessors: ARMTemplate, ARMParameters,
//     ResourcesSnapshot, PredictedResources (plus EnvLocation).
//   - ValidationCheckTypeProvision ("provision"): a provider-agnostic, lean
//     context carrying no ARM data — only ambient environment values.
//     Accessors: EnvName, SubscriptionID, EnvLocation, ResourceGroup,
//     TargetScope. These are best-effort and may be empty on a cold run (see
//     ValidationCheckTypeProvision for details).
type ValidationContext struct {
	// ContextID is the unique identifier for this context delivery.
	ContextID string
	// CheckType identifies the validation context (e.g. "arm-provision").
	CheckType string
	// Data is the reassembled context map (key → full value).
	Data map[string][]byte
}

// ResourcesSnapshot returns the raw Bicep snapshot JSON from the context.
func (c *ValidationContext) ResourcesSnapshot() ([]byte, bool) {
	v, ok := c.Data[ValidationContextResourcesSnapshot]
	return v, ok
}

// PredictedResources returns the predicted resources JSON array from the context.
func (c *ValidationContext) PredictedResources() ([]byte, bool) {
	v, ok := c.Data[ValidationContextPredictedResources]
	return v, ok
}

// ParsePredictedResources returns the predicted resources as typed structs.
// This is a convenience wrapper that parses the JSON array into []PredictedResource.
func (c *ValidationContext) ParsePredictedResources() ([]PredictedResource, error) {
	raw, ok := c.PredictedResources()
	if !ok {
		return nil, nil
	}
	var resources []PredictedResource
	if err := json.Unmarshal(raw, &resources); err != nil {
		return nil, err
	}
	return resources, nil
}

// ARMTemplate returns the compiled ARM template JSON from the context.
func (c *ValidationContext) ARMTemplate() ([]byte, bool) {
	v, ok := c.Data[ValidationContextARMTemplate]
	return v, ok
}

// ARMParameters returns the resolved ARM parameters JSON from the context.
func (c *ValidationContext) ARMParameters() ([]byte, bool) {
	v, ok := c.Data[ValidationContextARMParameters]
	return v, ok
}

// EnvLocation returns the Azure deployment location from the context. For
// "provision" checks this is a best-effort ambient value that may be empty on a
// cold first-time run, since the dispatch precedes the provider's location
// resolution/prompt.
func (c *ValidationContext) EnvLocation() (string, bool) {
	v, ok := c.Data[ValidationContextEnvLocation]
	if !ok {
		return "", false
	}
	return string(v), true
}

// EnvName returns the azd environment name from a "provision" check context.
func (c *ValidationContext) EnvName() (string, bool) {
	v, ok := c.Data[ValidationContextEnvName]
	if !ok {
		return "", false
	}
	return string(v), true
}

// SubscriptionID returns the Azure subscription id from a "provision" check
// context. This is a best-effort ambient value: the "provision" dispatch runs
// before the provider resolves/prompts for the subscription, so it may be empty
// on a cold first-time run. Treat an empty value as "not yet known".
func (c *ValidationContext) SubscriptionID() (string, bool) {
	v, ok := c.Data[ValidationContextSubscriptionID]
	if !ok {
		return "", false
	}
	return string(v), true
}

// ResourceGroup returns the target resource group name from a "provision" check
// context. The "provision" dispatch always includes the resource group key, so
// ok is true whenever the context originates from a provision check; the value
// is an empty string for subscription-scoped deployments (use TargetScope to
// distinguish scopes rather than relying on ok).
//
// This is a best-effort ambient value read from AZURE_RESOURCE_GROUP before the
// provider resolves/prompts for the resource group, so it may be empty on a
// cold run even for an RG-scoped template. Treat it as best-effort.
func (c *ValidationContext) ResourceGroup() (string, bool) {
	v, ok := c.Data[ValidationContextResourceGroup]
	if !ok {
		return "", false
	}
	return string(v), true
}

// TargetScope returns the deployment target scope ("subscription" or
// "resourceGroup") from a "provision" check context.
//
// This is best-effort, not authoritative: it is inferred solely from the
// presence of AZURE_RESOURCE_GROUP in the environment at dispatch time, before
// the provider (e.g. Bicep) determines the template's actual target scope. On a
// cold run it can report "subscription" for an RG-scoped template, or
// "resourceGroup" from a stale env var for a subscription-scoped deployment.
// Do not rely on it as the definitive scope.
func (c *ValidationContext) TargetScope() (string, bool) {
	v, ok := c.Data[ValidationContextTargetScope]
	if !ok {
		return "", false
	}
	return string(v), true
}

// ValidationCheckProvider is the extension-side interface for a validation check.
// Extensions implement this to provide custom checks that run during the azd
// validation pipeline (e.g. arm-provision during provisioning).
type ValidationCheckProvider interface {
	// Validate runs the check against the provided context and returns results.
	Validate(
		ctx context.Context,
		valCtx *ValidationContext,
		req *ValidationCheckRequest,
	) (*ValidationCheckResponse, error)
}

// ValidationCheckProviderFactory creates a new ValidationCheckProvider instance.
type ValidationCheckProviderFactory func() ValidationCheckProvider

// ValidationCheckRegistration describes a validation check to register with azd core.
type ValidationCheckRegistration struct {
	// CheckType identifies the validation context (e.g. "arm-provision").
	CheckType string
	// RuleID is a stable, unique identifier for this check rule.
	RuleID string
	// Factory creates a new provider instance.
	Factory ValidationCheckProviderFactory
}

// --- Check type constants ---

const (
	// ValidationCheckTypeArmProvision is the check type dispatched by the
	// Bicep provider during ARM-template provision validation. Its context
	// carries ARM-specific data (template, parameters, resource snapshot) and
	// therefore only runs for Bicep-provisioned deployments.
	ValidationCheckTypeArmProvision = "arm-provision"

	// ValidationCheckTypeProvision is the provider-agnostic check type
	// dispatched immediately before provisioning runs, regardless of the
	// provisioning provider (Bicep, Terraform, or extension-provided providers
	// such as microsoft.foundry and demo). Its context is "lean" because it
	// deliberately omits all of the ARM-derived data that "arm-provision"
	// carries — there is no ARM template, no resolved parameters, no resources
	// snapshot, and no predicted resources — since non-ARM providers do not
	// produce them. It carries only ambient environment values: the
	// environment name, subscription, location, resource group, and target
	// scope.
	//
	// These values are best-effort ambient values read from the azd
	// environment at dispatch time. The dispatch happens before the provider
	// resolves and prompts for subscription/location/resource group, so on a
	// cold first-time run (no flags, no persisted env values) subscription_id,
	// env_location, and resource_group may be empty, and target_scope is
	// inferred only from the presence of AZURE_RESOURCE_GROUP. Checks that
	// depend on these values must treat them as best-effort and tolerate empty
	// or not-yet-authoritative values (e.g. skip rather than fail when unset).
	ValidationCheckTypeProvision = "provision"
)

// --- Context key constants for "arm-provision" checks ---

const (
	// ValidationContextResourcesSnapshot is the key for the raw Bicep
	// snapshot JSON in an "arm-provision" check context.
	ValidationContextResourcesSnapshot = "resources_snapshot"
	// ValidationContextPredictedResources is the key for the JSON array of
	// predicted resources extracted from the Bicep snapshot. Each element is
	// a resource object with type, name, location, properties, etc.
	ValidationContextPredictedResources = "predicted_resources"
	// ValidationContextARMTemplate is the key for the compiled ARM
	// template JSON in an "arm-provision" check context.
	ValidationContextARMTemplate = "arm_template"
	// ValidationContextARMParameters is the key for the resolved ARM
	// parameters JSON in an "arm-provision" check context.
	ValidationContextARMParameters = "arm_parameters"
	// ValidationContextEnvLocation is the key for the Azure deployment
	// location string. It is present in both "arm-provision" and
	// "provision" check contexts.
	ValidationContextEnvLocation = "env_location"
)

// --- Context key constants for "provision" (provider-agnostic) checks ---

const (
	// ValidationContextEnvName is the key for the azd environment name in a
	// "provision" check context.
	ValidationContextEnvName = "env_name"
	// ValidationContextSubscriptionID is the key for the Azure subscription id
	// in a "provision" check context.
	ValidationContextSubscriptionID = "subscription_id"
	// ValidationContextResourceGroup is the key for the target resource group
	// name in a "provision" check context. It is empty for subscription-scoped
	// deployments.
	ValidationContextResourceGroup = "resource_group"
	// ValidationContextTargetScope is the key for the deployment target scope
	// ("subscription" or "resourceGroup") in a "provision" check context.
	ValidationContextTargetScope = "target_scope"
)
