// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package contracts

// EnvRefreshResult is the contract for the output of `azd env refresh`.
type EnvRefreshResult struct {
	Outputs   map[string]EnvRefreshOutputParameter `json:"outputs"`
	Resources []EnvRefreshResource                 `json:"resources"`
}

// EvnRefreshOutputType are the values for the "type" property of an output.
type EnvRefreshOutputType string

const (
	EnvRefreshOutputTypeBoolean EnvRefreshOutputType = "boolean"
	EnvRefreshOutputTypeString  EnvRefreshOutputType = "string"
	EnvRefreshOutputTypeNumber  EnvRefreshOutputType = "number"
	EnvRefreshOutputTypeObject  EnvRefreshOutputType = "object"
	EnvRefreshOutputTypeArray   EnvRefreshOutputType = "array"
)

// EnvRefreshOutputParameter is the contract for the value in the "outputs" map
// of and EnvRefreshResult.
type EnvRefreshOutputParameter struct {
	Type  EnvRefreshOutputType `json:"type"`
	Value any                  `json:"value"`
}

// EnvRefreshResource is the contract for a resource in the "resources" array
type EnvRefreshResource struct {
	Id string `json:"id"`
}
