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
	SKU json.RawMessage `json:"sku,omitzero"`
	// Tags holds resource tags as raw JSON (may be a map or ARM expression).
	Tags json.RawMessage `json:"tags,omitzero"`
	// Scope is used for extension or cross-scope resources.
	Scope string `json:"scope,omitempty"`
}

// ValidationContext holds the assembled context data for a validation check.
// It is populated from PrepareValidationContextChunk messages and injected
// into the provider's Validate call by the ValidationManager.
type ValidationContext struct {
	// ContextID is the unique identifier for this context delivery.
	ContextID string
	// CheckType identifies the validation context (e.g. "local-preflight").
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

// EnvLocation returns the Azure deployment location from the context.
func (c *ValidationContext) EnvLocation() (string, bool) {
	v, ok := c.Data[ValidationContextEnvLocation]
	if !ok {
		return "", false
	}
	return string(v), true
}

// ValidationCheckProvider is the extension-side interface for a validation check.
// Extensions implement this to provide custom checks that run during azd's
// validation pipeline (e.g. local-preflight during provisioning).
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
	// CheckType identifies the validation context (e.g. "local-preflight").
	CheckType string
	// RuleID is a stable, unique identifier for this check rule.
	RuleID string
	// Factory creates a new provider instance.
	Factory ValidationCheckProviderFactory
}

// --- Context key constants for "local-preflight" checks ---

const (
	// ValidationContextResourcesSnapshot is the key for the raw Bicep
	// snapshot JSON in a "local-preflight" check context.
	ValidationContextResourcesSnapshot = "resources_snapshot"
	// ValidationContextPredictedResources is the key for the JSON array of
	// predicted resources extracted from the Bicep snapshot. Each element is
	// a resource object with type, name, location, properties, etc.
	ValidationContextPredictedResources = "predicted_resources"
	// ValidationContextARMTemplate is the key for the compiled ARM
	// template JSON in a "local-preflight" check context.
	ValidationContextARMTemplate = "arm_template"
	// ValidationContextARMParameters is the key for the resolved ARM
	// parameters JSON in a "local-preflight" check context.
	ValidationContextARMParameters = "arm_parameters"
	// ValidationContextEnvLocation is the key for the Azure deployment
	// location string in a "local-preflight" check context.
	ValidationContextEnvLocation = "env_location"
)
