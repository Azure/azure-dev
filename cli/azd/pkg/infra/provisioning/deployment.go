// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import "errors"

var (
	ErrDeploymentsNotFound = errors.New("no deployments found")
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

type Resource struct {
	Id string
}

func (p *InputParameter) HasValue() bool {
	return p.Value != nil
}

func (p *InputParameter) HasDefaultValue() bool {
	return p.DefaultValue != nil
}
