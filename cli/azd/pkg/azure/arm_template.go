// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"encoding/json"
)

// RawArmTemplate is a JSON encoded ARM template.
type RawArmTemplate json.RawMessage

// ArmTemplate represents an Azure Resource Manager deployment template. It follows the structure outlined
// at https://learn.microsoft.com/azure/azure-resource-manager/templates/syntax, but only exposes portions of the
// object that azd cares about.
type ArmTemplate struct {
	Schema         string                `json:"$schema"`
	ContentVersion string                `json:"contentVersion"`
	Parameters     ArmTemplateParameters `json:"parameters"`
	Outputs        ArmTemplateOutputs    `json:"outputs"`
}

type ArmTemplateParameters map[string]ArmTemplateParameter

type ArmTemplateOutputs map[string]ArmTemplateOutput

type ArmTemplateParameter struct {
	Type         string         `json:"type"`
	DefaultValue any            `json:"defaultValue"`
	Metadata     map[string]any `json:"metadata"`
}

// Description returns the value of the "Description" string metadata for this parameter or empty if it can not be found.
func (p ArmTemplateParameter) Description() (d string, has bool) {
	if v, has := p.Metadata["description"]; has {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return "", false
}

type ArmTemplateOutput struct {
	Type     string         `json:"type"`
	Value    any            `json:"value"`
	Metadata map[string]any `json:"metadata"`
}

// Description returns the value of the "Description" string metadata for this output or empty if it can not be found.
func (o ArmTemplateOutput) Description() (d string, has bool) {
	if v, has := o.Metadata["description"]; has {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return "", false
}
