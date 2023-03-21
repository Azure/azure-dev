// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

// ArmParameters is a map of arm template parameters to their configured values.
type ArmParameters map[string]ArmParameterValue

// ArmParametersFile is the model type for a `.parameters.json` file. It fits the schema outlined here:
// https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json
type ArmParameterFile struct {
	Schema         string        `json:"$schema"`
	ContentVersion string        `json:"contentVersion"`
	Parameters     ArmParameters `json:"parameters"`
}

// ArmParameterValue wraps the configured value for the parameter.
type ArmParameterValue struct {
	Value any `json:"value"`
}
