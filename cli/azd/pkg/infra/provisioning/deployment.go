// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

type Deployment struct {
	Parameters map[string]InputParameter
	Outputs    map[string]OutputParameter
}

type InputParameter struct {
	Type         string
	DefaultValue interface{}
	Value        interface{}
}

type OutputParameter struct {
	Type  string
	Value interface{}
}

func (p *InputParameter) HasValue() bool {
	return p.Value != nil
}

func (p *InputParameter) HasDefaultValue() bool {
	return p.DefaultValue != nil
}
