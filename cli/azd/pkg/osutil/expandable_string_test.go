// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestExpandableStringYaml(t *testing.T) {
	var e *ExpandableString

	err := yaml.Unmarshal([]byte(`"${foo}"`), &e)
	assert.NoError(t, err)

	assert.Equal(t, "${foo}", e.Template)

	marshalled, err := yaml.Marshal(e)
	assert.NoError(t, err)

	assert.Equal(t, "${foo}\n", string(marshalled))
}

func TestNestedObjectYaml(t *testing.T) {
	expected := "a: ${foo}\nb: ${bar}\n"

	var custom *Custom
	err := yaml.Unmarshal([]byte(expected), &custom)
	assert.NoError(t, err)
	assert.Equal(t, "${foo}", custom.A.Template)
	assert.Equal(t, "${bar}", custom.B.Template)

	marshalled, err := yaml.Marshal(custom)
	assert.NoError(t, err)

	assert.Equal(t, expected, string(marshalled))
}

func TestNestedObjectWithEmpty(t *testing.T) {
	expected := "{}\n"

	var custom *Custom
	err := yaml.Unmarshal([]byte(expected), &custom)
	assert.NoError(t, err)
	assert.NotNil(t, custom.A)
	assert.NotNil(t, custom.B)

	marshalled, err := yaml.Marshal(custom)
	assert.NoError(t, err)

	assert.Equal(t, expected, string(marshalled))
}

type Custom struct {
	A ExpandableString `yaml:"a,omitempty"`
	B ExpandableString `yaml:"b,omitempty"`
}
