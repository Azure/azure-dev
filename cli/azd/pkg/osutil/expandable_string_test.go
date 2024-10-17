// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"testing"

	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
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

func TestExpandableString_Empty(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		e := NewExpandableString("")
		assert.True(t, e.Empty())
	})

	t.Run("NonEmpty", func(t *testing.T) {
		e := NewExpandableString("${ENV_VAR}")
		assert.False(t, e.Empty())
	})
}
