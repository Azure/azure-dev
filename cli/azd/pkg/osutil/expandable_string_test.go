// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestExpandableStringYaml(t *testing.T) {
	var e ExpandableString

	err := yaml.Unmarshal([]byte(`"${foo}"`), &e)
	assert.NoError(t, err)

	assert.Equal(t, "${foo}", e.template)

	marshalled, err := yaml.Marshal(e)
	assert.NoError(t, err)

	assert.Equal(t, "${foo}\n", string(marshalled))
}

func TestNestedObjectYaml(t *testing.T) {
	c := Custom{
		A: &ExpandableString{
			template: "${foo}",
		},
		B: &ExpandableString{
			template: "${bar}",
		},
	}

	marshalled, err := yaml.Marshal(c)
	assert.NoError(t, err)

	assert.Equal(t, "a: ${foo}\nb: ${bar}\n", string(marshalled))
}

type Custom struct {
	A *ExpandableString `yaml:"a,omitempty"`
	B *ExpandableString `yaml:"b,omitempty"`
}
