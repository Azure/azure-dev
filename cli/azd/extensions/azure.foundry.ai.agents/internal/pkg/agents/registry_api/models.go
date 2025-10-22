// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package registry_api

import (
	"azureaiagent/internal/pkg/agents/agent_api"
	"encoding/json"
)

// Manifest represents an agent manifest from the Azure ML registry
type Manifest struct {
	// AssetId of Manifest
	ID string `json:"id"`

	Name string `json:"name"`

	Version string `json:"version"`

	Type string `json:"type"`

	// The display name of the Manifest to be used in the UI.
	// This may differ from the Name property.
	DisplayName string `json:"displayName"`

	// The manifest description text
	Description string `json:"description"`

	// Tag dictionary. Tags can be added, removed, and updated.
	Tags map[string]string `json:"tags"`

	// Only required for manifests in the public catalog
	CatalogData json.RawMessage `json:"catalogData,omitempty"`

	// AgentV2 schema
	Template agent_api.PromptAgentDefinition `json:"template"`

	// OpenAPI parameter definitions
	Parameters map[string]OpenApiParameter `json:"parameters"`

	SystemData json.RawMessage `json:"systemData,omitempty"`
}

// OpenApiSchema represents an OpenAPI schema definition
// Based on Microsoft.OpenApi.Models.OpenApiSchema from .NET OpenAPI library
type OpenApiSchema struct {
	// Schema Object properties following JSON Schema definition

	// Basic metadata
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description,omitempty"`
	Default     interface{} `json:"default,omitempty"`
	Example     interface{} `json:"example,omitempty"`

	// Type and format
	Type   string `json:"type,omitempty"`
	Format string `json:"format,omitempty"`

	// Numeric validation
	Minimum          *float64 `json:"minimum,omitempty"`
	Maximum          *float64 `json:"maximum,omitempty"`
	ExclusiveMinimum bool     `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum bool     `json:"exclusiveMaximum,omitempty"`
	MultipleOf       *float64 `json:"multipleOf,omitempty"`

	// String validation
	MinLength *int   `json:"minLength,omitempty"`
	MaxLength *int   `json:"maxLength,omitempty"`
	Pattern   string `json:"pattern,omitempty"`

	// Array validation
	Items       *OpenApiSchema `json:"items,omitempty"`
	MinItems    *int           `json:"minItems,omitempty"`
	MaxItems    *int           `json:"maxItems,omitempty"`
	UniqueItems bool           `json:"uniqueItems,omitempty"`

	// Object validation
	Properties           map[string]*OpenApiSchema `json:"properties,omitempty"`
	AdditionalProperties interface{}               `json:"additionalProperties,omitempty"` // can be bool or schema
	Required             []string                  `json:"required,omitempty"`
	MinProperties        *int                      `json:"minProperties,omitempty"`
	MaxProperties        *int                      `json:"maxProperties,omitempty"`

	// Enumeration
	Enum []interface{} `json:"enum,omitempty"`

	// Composition keywords
	AllOf []*OpenApiSchema `json:"allOf,omitempty"`
	OneOf []*OpenApiSchema `json:"oneOf,omitempty"`
	AnyOf []*OpenApiSchema `json:"anyOf,omitempty"`
	Not   *OpenApiSchema   `json:"not,omitempty"`

	// OpenAPI-specific properties
	Nullable      bool   `json:"nullable,omitempty"`
	Discriminator string `json:"discriminator,omitempty"`
	ReadOnly      bool   `json:"readOnly,omitempty"`
	WriteOnly     bool   `json:"writeOnly,omitempty"`
	Deprecated    bool   `json:"deprecated,omitempty"`

	// External documentation
	ExternalDocs map[string]interface{} `json:"externalDocs,omitempty"`

	// XML metadata
	XML map[string]interface{} `json:"xml,omitempty"`

	// Reference for $ref usage
	Reference string `json:"$ref,omitempty"`

	// Extensions (handled specially in OpenAPI)
	Extensions map[string]interface{} `json:"-"`
}

// OpenApiParameter represents an OpenAPI parameter definition
// Based on Microsoft.OpenApi.Models.OpenApiParameter from .NET OpenAPI library
type OpenApiParameter struct {
	// REQUIRED. The name of the parameter. Parameter names are case sensitive.
	Name string `json:"name,omitempty"`

	// REQUIRED. The location of the parameter. Possible values are "query", "header", "path" or "cookie".
	In string `json:"in,omitempty"`

	// A brief description of the parameter. This could contain examples of use.
	Description string `json:"description,omitempty"`

	// Determines whether this parameter is mandatory.
	// If the parameter location is "path", this property is REQUIRED and its value MUST be true.
	Required bool `json:"required,omitempty"`

	// Specifies that a parameter is deprecated and SHOULD be transitioned out of usage.
	Deprecated bool `json:"deprecated,omitempty"`

	// Sets the ability to pass empty-valued parameters. This is valid only for query parameters.
	AllowEmptyValue bool `json:"allowEmptyValue,omitempty"`

	// Describes how the parameter value will be serialized depending on the type of the parameter value.
	Style string `json:"style,omitempty"`

	// When this is true, parameter values of type array or object generate separate parameters
	// for each value of the array or key-value pair of the map.
	Explode bool `json:"explode,omitempty"`

	// Determines whether the parameter value SHOULD allow reserved characters.
	AllowReserved bool `json:"allowReserved,omitempty"`

	// The schema defining the type used for the parameter.
	Schema *OpenApiSchema `json:"schema,omitempty"`

	// Example of the media type. The example SHOULD match the specified schema.
	Example interface{} `json:"example,omitempty"`

	// Examples of the media type. Each example SHOULD contain a value in the correct format.
	Examples map[string]interface{} `json:"examples,omitempty"`

	// A map containing the representations for the parameter. The key is the media type.
	Content map[string]interface{} `json:"content,omitempty"`

	// Reference object for $ref usage
	Reference string `json:"$ref,omitempty"`

	// This object MAY be extended with Specification Extensions.
	Extensions map[string]interface{} `json:"-"` // Extensions are handled specially in OpenAPI
}

// ManifestList represents a list of manifests returned from GetAllLatest
type ManifestList struct {
	Value []Manifest `json:"value"`
	// Add pagination fields if needed
	NextLink string `json:"nextLink,omitempty"`
}
