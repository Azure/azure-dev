// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
)

type Deployment struct {
	Parameters map[string]InputParameter
	Outputs    map[string]OutputParameter
}

type ParameterType string

const (
	ParameterTypeString  ParameterType = "string"
	ParameterTypeNumber  ParameterType = "number"
	ParameterTypeBoolean ParameterType = "bool"
	ParameterTypeObject  ParameterType = "object"
	ParameterTypeArray   ParameterType = "array"
)

// ParameterTypeFromArmType maps an ARM parameter type to a provisioning.ParameterType.
//
// Panics if the provided armType is not recognized.
func ParameterTypeFromArmType(armType string) ParameterType {
	switch armType {
	case "String", "string", "secureString", "securestring":
		return ParameterTypeString
	case "Bool", "bool":
		return ParameterTypeBoolean
	case "Int", "int":
		return ParameterTypeNumber
	case "Object", "object", "secureObject", "secureobject":
		return ParameterTypeObject
	case "Array", "array":
		return ParameterTypeArray
	default:
		panic(fmt.Sprintf("unexpected arm type: '%s'", armType))
	}
}

type InputParameter struct {
	Type         string
	DefaultValue interface{}
	Value        interface{}
}

type OutputParameter struct {
	Type  ParameterType
	Value interface{}
}

// OutputParametersFromArmOutputs converts the outputs from an ARM deployment to a map of provisioning.OutputParameter.
//
// The casing of the output parameter names will match the casing in the provided templateOutputs. If an output is
// present in azureOutputParams but not in templateOutputs, the output name will be upper-cased to work around
// inconsistent casing behavior in Azure (e.g. `azurE_RESOURCE_GROUP`).
//
// Secured outputs are skipped and not included in the result.
func OutputParametersFromArmOutputs(
	templateOutputs azure.ArmTemplateOutputs,
	azureOutputParams map[string]azapi.AzCliDeploymentOutput) map[string]OutputParameter {
	canonicalOutputCasings := make(map[string]string, len(templateOutputs))

	for key := range templateOutputs {
		canonicalOutputCasings[strings.ToLower(key)] = key
	}

	outputParams := make(map[string]OutputParameter, len(azureOutputParams))

	for key, azureParam := range azureOutputParams {
		if azureParam.Secured() {
			// Secured output can't be retrieved, so we skip it.
			// https://learn.microsoft.com/azure/azure-resource-manager/bicep/outputs?tabs=azure-powershell#secure-outputs
			log.Println("Skipping secured output parameter:", key)
			continue
		}
		var paramName string
		canonicalCasing, found := canonicalOutputCasings[strings.ToLower(key)]
		if found {
			paramName = canonicalCasing
		} else {
			// To support BYOI (bring your own infrastructure) scenarios we will default to UPPER when canonical casing
			// is not found in the parameters file to workaround strange azure behavior with OUTPUT values that look
			// like `azurE_RESOURCE_GROUP`
			paramName = strings.ToUpper(key)
		}

		outputParams[paramName] = OutputParameter{
			Type:  ParameterTypeFromArmType(azureParam.Type),
			Value: azureParam.Value,
		}
	}

	return outputParams
}

// State represents the "current state" of the infrastructure, which is the result of the most recent deployment. For ARM
// this corresponds to information from the most recent deployment object. For Terraform, it's information from the state
// file.
type State struct {
	// Outputs from the most recent deployment.
	Outputs map[string]OutputParameter
	// The resources that make up the application.
	Resources []Resource
}

// MergeInto merges other on top of s, i.e. if a key exists in both s and other, the value from other will be used.
func (s *State) MergeInto(other State) {
	if s.Outputs == nil {
		s.Outputs = make(map[string]OutputParameter)
	}

	maps.Copy(s.Outputs, other.Outputs)

	for _, res := range other.Resources {
		if i := slices.IndexFunc(s.Resources, func(r Resource) bool { return r.Id == res.Id }); i != -1 {
			s.Resources[i] = res
			continue
		}

		s.Resources = append(s.Resources, res)
	}
}

type Resource struct {
	Id string
}

func (p *InputParameter) HasValue() bool {
	return p.Value != nil
}

func (p *InputParameter) HasDefaultValue() bool {
	return p.DefaultValue != nil
}
