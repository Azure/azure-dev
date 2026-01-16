// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

type DeploymentScope string

const DeploymentScopeSubscription DeploymentScope = "subscription"
const DeploymentScopeResourceGroup DeploymentScope = "resourceGroup"

// RawArmTemplate is a JSON encoded ARM template.
type RawArmTemplate = json.RawMessage

// ArmTemplate represents an Azure Resource Manager deployment template. It follows the structure outlined
// at https://learn.microsoft.com/azure/azure-resource-manager/templates/syntax, but only exposes portions of the
// object that azd cares about.
type ArmTemplate struct {
	Schema         string                          `json:"$schema"`
	ContentVersion string                          `json:"contentVersion"`
	Parameters     ArmTemplateParameterDefinitions `json:"parameters"`
	Outputs        ArmTemplateOutputs              `json:"outputs"`
	Definitions    ArmTemplateParameterDefinitions `json:"definitions"`
}

// TargetScope uses the $schema property of the template to determine what scope this template should be deployed
// at or an error if the scope could not be determined.
func (t ArmTemplate) TargetScope() (DeploymentScope, error) {
	if t.Schema == "" {
		return DeploymentScope(""), errors.New("no schema in template")
	}

	u, err := url.Parse(t.Schema)
	if err != nil {
		return DeploymentScope(""), fmt.Errorf("error parsing schema: %w", err)
	}

	switch {
	case strings.EqualFold(path.Base(u.Path), "subscriptionDeploymentTemplate.json"):
		return DeploymentScopeSubscription, nil
	case strings.EqualFold(path.Base(u.Path), "deploymentTemplate.json"):
		return DeploymentScopeResourceGroup, nil
	default:
		return DeploymentScope(""), fmt.Errorf("unknown schema: %s", t.Schema)
	}
}

type ArmTemplateParameterDefinitions map[string]ArmTemplateParameterDefinition

type ArmTemplateOutputs map[string]ArmTemplateOutput

type ArmTemplateParameterAdditionalPropertiesProperties struct {
	Type      string                     `json:"type"`
	MinValue  *int                       `json:"minValue,omitempty"`
	MaxValue  *int                       `json:"maxValue,omitempty"`
	MinLength *int                       `json:"minLength,omitempty"`
	MaxLength *int                       `json:"maxLength,omitempty"`
	Metadata  map[string]json.RawMessage `json:"metadata"`
}

type ArmTemplateParameterAdditionalPropertiesValue struct {
	props *ArmTemplateParameterAdditionalPropertiesProperties
}

func (v ArmTemplateParameterAdditionalPropertiesValue) HasAdditionalProperties() bool {
	return v.props != nil
}

func (v ArmTemplateParameterAdditionalPropertiesValue) Properties() ArmTemplateParameterAdditionalPropertiesProperties {
	return *v.props
}

func (v *ArmTemplateParameterAdditionalPropertiesValue) UnmarshalJSON(data []byte) error {
	if string(data) == "false" {
		return nil
	}

	var props ArmTemplateParameterAdditionalPropertiesProperties
	if err := json.Unmarshal(data, &props); err != nil {
		return err
	}

	v.props = &props
	return nil
}

func (v *ArmTemplateParameterAdditionalPropertiesValue) MarshalJSON() ([]byte, error) {
	if v.props == nil {
		return []byte("false"), nil
	}

	return json.Marshal(v.props)
}

type ArmTemplateParameterDefinition struct {
	Type                 string                                         `json:"type"`
	DefaultValue         any                                            `json:"defaultValue"`
	AllowedValues        *[]any                                         `json:"allowedValues,omitempty"`
	MinValue             *int                                           `json:"minValue,omitempty"`
	MaxValue             *int                                           `json:"maxValue,omitempty"`
	MinLength            *int                                           `json:"minLength,omitempty"`
	MaxLength            *int                                           `json:"maxLength,omitempty"`
	Metadata             map[string]json.RawMessage                     `json:"metadata"`
	Ref                  string                                         `json:"$ref"`
	Properties           ArmTemplateParameterDefinitions                `json:"properties,omitempty"`
	AdditionalProperties *ArmTemplateParameterAdditionalPropertiesValue `json:"additionalProperties,omitempty"`
	Nullable             *bool                                          `json:"nullable,omitempty"`
}

func (d *ArmTemplateParameterDefinition) Secure() bool {
	return IsSecuredARMType(d.Type)
}

func IsSecuredARMType(t string) bool {
	lowerCase := strings.ToLower(t)
	return lowerCase == "secureobject" || lowerCase == "securestring"
}

type AutoGenInput struct {
	Length     uint  `json:"length,omitempty"`
	NoLower    *bool `json:"noLower,omitempty"`
	NoUpper    *bool `json:"noUpper,omitempty"`
	NoNumeric  *bool `json:"noNumeric,omitempty"`
	NoSpecial  *bool `json:"noSpecial,omitempty"`
	MinLower   *uint `json:"minLower,omitempty"`
	MinUpper   *uint `json:"minUpper,omitempty"`
	MinNumeric *uint `json:"minNumeric,omitempty"`
	MinSpecial *uint `json:"minSpecial,omitempty"`
}

type AzdMetadataType string

const AzdMetadataTypeLocation AzdMetadataType = "location"
const AzdMetadataTypeGenerate AzdMetadataType = "generate"
const AzdMetadataTypeGenerateOrManual AzdMetadataType = "generateOrManual"
const AzdMetadataTypeResourceGroup AzdMetadataType = "resourceGroup"

type AzdMetadata struct {
	Type               *AzdMetadataType `json:"type,omitempty"`
	AutoGenerateConfig *AutoGenInput    `json:"config,omitempty"`
	DefaultValueExpr   *string          `json:"defaultValueExpr,omitempty"`
	Default            any              `json:"default,omitempty"`
	UsageName          usageName        `json:"usageName,omitempty"`
}

// usageName is a custom type that can be either a single string or an array of strings.
// Enables unmarshalling from both formats, so user can set one only usageName like usageName: "foo" or
// multiple usageNames like usageName: ["foo", "bar"].
type usageName []string

func (u *usageName) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*u = usageName{single}
		return nil
	}

	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return err
	}

	*u = usageName(multiple)
	return nil
}

// Description returns the value of the "Description" string metadata for this parameter or empty if it can not be found.
func (p ArmTemplateParameterDefinition) Description() (string, bool) {
	if v, has := p.Metadata["description"]; has {
		var description string
		if err := json.Unmarshal(v, &description); err == nil {
			return description, true
		}
	}

	return "", false
}

// AzdMetadata returns the value of the "azd" object metadata for this parameter or the zero value if it can not be found.
func (p ArmTemplateParameterDefinition) AzdMetadata() (AzdMetadata, bool) {
	if v, has := p.Metadata["azd"]; has {
		var metadata AzdMetadata
		if err := json.Unmarshal(v, &metadata); err == nil {
			return metadata, true
		}
	}

	return AzdMetadata{}, false
}

type ArmTemplateOutput struct {
	Type     string         `json:"type"`
	Value    any            `json:"value"`
	Metadata map[string]any `json:"metadata"`
	Ref      string         `json:"$ref"`
}

// ArmTemplateResource represents a resource in an ARM template
type ArmTemplateResource struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Location any    `json:"location,omitempty"`
}

// ExtractResourceTypes extracts unique resource types from a compiled ARM template.
// Returns a list of resource types in the format "Microsoft.Provider/resourceType".
func ExtractResourceTypes(rawTemplate RawArmTemplate) ([]string, error) {
	var templateWithResources struct {
		Resources []ArmTemplateResource `json:"resources"`
	}

	if err := json.Unmarshal(rawTemplate, &templateWithResources); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ARM template: %w", err)
	}

	// Use a map to track unique resource types
	uniqueTypes := make(map[string]struct{})
	for _, resource := range templateWithResources.Resources {
		if resource.Type != "" {
			uniqueTypes[resource.Type] = struct{}{}
		}
	}

	// Convert map to slice
	resourceTypes := make([]string, 0, len(uniqueTypes))
	for resourceType := range uniqueTypes {
		resourceTypes = append(resourceTypes, resourceType)
	}

	return resourceTypes, nil
}
