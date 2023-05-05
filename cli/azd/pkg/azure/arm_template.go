// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
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
}

func (t ArmTemplate) TargetScope() DeploymentScope {
	u, err := url.Parse(t.Schema)
	if err != nil {
		panic(fmt.Sprintf("bad schema for template: %s", t.Schema))
	}

	switch path.Base(u.Path) {
	case "subscriptionDeploymentTemplate.json":
		return DeploymentScopeSubscription
	case "deploymentTemplate.json":
		return DeploymentScopeResourceGroup
	default:
		panic(fmt.Sprintf("unknown schema type: %s", path.Base(u.Path)))
	}
}

type ArmTemplateParameterDefinitions map[string]ArmTemplateParameterDefinition

type ArmTemplateOutputs map[string]ArmTemplateOutput

type ArmTemplateParameterDefinition struct {
	Type          string                     `json:"type"`
	DefaultValue  any                        `json:"defaultValue"`
	AllowedValues *[]any                     `json:"allowedValues,omitempty"`
	MinValue      *int                       `json:"minValue,omitempty"`
	MaxValue      *int                       `json:"maxValue,omitempty"`
	MinLength     *int                       `json:"minLength,omitempty"`
	MaxLength     *int                       `json:"maxLength,omitempty"`
	Metadata      map[string]json.RawMessage `json:"metadata"`
}

func (d *ArmTemplateParameterDefinition) Secure() bool {
	return d.Type == "secureObject" || d.Type == "secureString"
}

type AzdMetadata struct {
	Type *string `json:"type,omitempty"`
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
}
