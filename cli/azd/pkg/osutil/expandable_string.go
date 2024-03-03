// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"fmt"

	"github.com/drone/envsubst"
	"gopkg.in/yaml.v3"
)

func NewExpandableString(template string) ExpandableString {
	return ExpandableString{
		Template: template,
	}
}

// ExpandableString is a string that has ${foo} style references inside which can be evaluated.
type ExpandableString struct {
	Template string `yaml:",inline"`
}

// Envsubst evaluates the template, substituting values as [envsubst.Eval] would.
func (e *ExpandableString) Envsubst(mapping func(string) string) (string, error) {
	return envsubst.Eval(e.Template, mapping)
}

// MustEnvsubst evaluates the template, substituting values as [envsubst.Eval] would and panics if there
// is an error (for example, the string is malformed).
func (e *ExpandableString) MustEnvsubst(mapping func(string) string) string {
	if v, err := envsubst.Eval(e.Template, mapping); err != nil {
		panic(fmt.Sprintf("MustEnvsubst: %v", err))
	} else {
		return v
	}
}

func (e ExpandableString) MarshalText() ([]byte, error) {
	return []byte(e.Template), nil
}

func (e *ExpandableString) UnmarshalYAML(node *yaml.Node) error {
	var value any
	if err := node.Decode(&value); err != nil {
		return err
	}

	if str, ok := value.(string); ok {
		e.Template = str
	}

	return nil
}
