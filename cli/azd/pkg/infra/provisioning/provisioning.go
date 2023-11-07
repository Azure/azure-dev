// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
)

// NewEnvRefreshResultFromState creates a EnvRefreshResult from a provisioning state object,
// applying the required translations.
func NewEnvRefreshResultFromState(state *State) contracts.EnvRefreshResult {
	result := contracts.EnvRefreshResult{}

	result.Outputs = make(map[string]contracts.EnvRefreshOutputParameter, len(state.Outputs))
	result.Resources = make([]contracts.EnvRefreshResource, len(state.Resources))

	mapType := func(p ParameterType) contracts.EnvRefreshOutputType {
		switch p {
		case ParameterTypeString:
			return contracts.EnvRefreshOutputTypeString
		case ParameterTypeBoolean:
			return contracts.EnvRefreshOutputTypeBoolean
		case ParameterTypeNumber:
			return contracts.EnvRefreshOutputTypeNumber
		case ParameterTypeObject:
			return contracts.EnvRefreshOutputTypeObject
		case ParameterTypeArray:
			return contracts.EnvRefreshOutputTypeArray
		default:
			panic(fmt.Sprintf("unknown provisioning.ParameterType value: %v", p))
		}
	}

	for k, v := range state.Outputs {
		result.Outputs[k] = contracts.EnvRefreshOutputParameter{
			Type:  mapType(v.Type),
			Value: v.Value,
		}
	}

	for idx, res := range state.Resources {
		result.Resources[idx] = contracts.EnvRefreshResource{
			Id: res.Id,
		}
	}

	return result
}

// Parses the specified IaC Provider to ensure whether it is valid or not
// Defaults to `Bicep` if no provider is specified
func ParseProvider(kind ProviderKind) (ProviderKind, error) {
	switch kind {
	// For the time being we need to include `Test` here for the unit tests to work as expected
	// App builds will pass this test but fail resolving the provider since `Test` won't be registered in the container
	case NotSpecified, Bicep, Terraform, Test:
		return kind, nil
	}

	return ProviderKind(""), fmt.Errorf("unsupported IaC provider '%s'", kind)
}
