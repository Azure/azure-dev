// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

func UpdateEnvironment(env *environment.Environment, outputs map[string]OutputParameter) error {
	if len(outputs) > 0 {
		for key, param := range outputs {
			// Complex types marshalled as JSON strings, simple types marshalled as simple strings
			if param.Type == ParameterTypeArray || param.Type == ParameterTypeObject {
				bytes, err := json.Marshal(param.Value)
				if err != nil {
					return fmt.Errorf("invalid value for output parameter '%s' (%s): %w", key, string(param.Type), err)
				}
				env.Values[key] = string(bytes)
			} else {
				env.Values[key] = fmt.Sprintf("%v", param.Value)
			}
		}

		if err := env.Save(); err != nil {
			return fmt.Errorf("writing environment: %w", err)
		}
	}

	return nil
}

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
