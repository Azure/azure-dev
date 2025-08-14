// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"maps"
	"slices"
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

type InputParameter struct {
	Type         string
	DefaultValue interface{}
	Value        interface{}
}

type OutputParameter struct {
	Type  ParameterType
	Value interface{}
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
