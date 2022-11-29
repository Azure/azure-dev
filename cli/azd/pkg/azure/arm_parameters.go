// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

type ArmParameters map[string]ArmParameterValue

type ArmParameterFile struct {
	Schema         string        `json:"$schema"`
	ContentVersion string        `json:"contentVersion"`
	Parameters     ArmParameters `json:"parameters"`
}

type ArmParameterValue struct {
	Value any `json:"value"`
}
