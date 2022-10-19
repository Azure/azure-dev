package contracts

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

const (
	EnvRefreshResultType = "envRefreshResult"
)

// EnvRefreshResult is the contract for the output of `azd env refresh`.
type EnvRefreshResult struct {
	Type      string                               `json:"type"`
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

// NewEnvRefreshResultFromProvisioningState creates a EnvRefreshResult from a provisioning state object,
// applying the required translations.
func NewEnvRefreshResultFromProvisioningState(state *provisioning.State) EnvRefreshResult {
	result := EnvRefreshResult{
		Type: EnvRefreshResultType,
	}

	result.Outputs = make(map[string]EnvRefreshOutputParameter, len(state.Outputs))
	result.Resources = make([]EnvRefreshResource, len(state.Resources))

	mapType := func(p provisioning.ParameterType) EnvRefreshOutputType {
		switch p {
		case provisioning.ParameterTypeString:
			return EnvRefreshOutputTypeString
		case provisioning.ParameterTypeBoolean:
			return EnvRefreshOutputTypeBoolean
		case provisioning.ParameterTypeNumber:
			return EnvRefreshOutputTypeNumber
		case provisioning.ParameterTypeObject:
			return EnvRefreshOutputTypeObject
		case provisioning.ParameterTypeArray:
			return EnvRefreshOutputTypeArray
		default:
			panic(fmt.Sprintf("unknown provisioning.ParameterType value: %v", p))
		}
	}

	for k, v := range state.Outputs {
		result.Outputs[k] = EnvRefreshOutputParameter{
			Type:  mapType(v.Type),
			Value: v.Value,
		}
	}

	for idx, res := range state.Resources {
		result.Resources[idx] = EnvRefreshResource{
			Id: res.Id,
		}
	}

	return result
}
