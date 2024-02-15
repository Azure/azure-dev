// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"

	"github.com/drone/envsubst"
)

func NewExpandableString(template string) ExpandableString {
	return ExpandableString{
		template: template,
	}
}

// ExpandableString is a string that has ${foo} style references inside which can be evaluated.
type ExpandableString struct {
	template string
}

// Envsubst evaluates the template, substituting values as [envsubst.Eval] would.
func (e ExpandableString) Envsubst(mapping func(string) string) (string, error) {
	return envsubst.Eval(e.template, mapping)
}

// MustEnvsubst evaluates the template, substituting values as [envsubst.Eval] would and panics if there
// is an error (for example, the string is malformed).
func (e ExpandableString) MustEnvsubst(mapping func(string) string) string {
	if v, err := envsubst.Eval(e.template, mapping); err != nil {
		panic(fmt.Sprintf("MustEnvsubst: %v", err))
	} else {
		return v
	}
}

func (e ExpandableString) MarshalYAML() (interface{}, error) {
	return e.template, nil
}

func (e *ExpandableString) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	e.template = s
	return nil
}
