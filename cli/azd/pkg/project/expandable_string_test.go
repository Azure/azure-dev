// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

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
