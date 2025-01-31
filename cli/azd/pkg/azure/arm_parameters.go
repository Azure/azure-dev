// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

// ArmParameters is a map of arm template parameters to their configured values.
type ArmParameters map[string]ArmParameter

// ArmParametersFile is the model type for a `.parameters.json` file. It fits the schema outlined here:
// https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json
type ArmParameterFile struct {
	Schema         string        `json:"$schema"`
	ContentVersion string        `json:"contentVersion"`
	Parameters     ArmParameters `json:"parameters"`
}

// ArmParameter wraps the configured value or KV reference for the parameter.
type ArmParameter struct {
	Value             any                         `json:"value"`
	KeyVaultReference *KeyVaultParameterReference `json:"reference"`
}

// KeyVaultParameterReference is the model type for a Key Vault parameter reference.
type KeyVaultParameterReference struct {
	KeyVault      KeyVaultReference `json:"keyVault"`
	SecretName    string            `json:"secretName"`
	SecretVersion string            `json:"secretVersion"`
}

// KeyVaultReference represents the Key Vault resource ID.
type KeyVaultReference struct {
	ID string `json:"id"`
}
