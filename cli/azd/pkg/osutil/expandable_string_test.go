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

func TestNewLiteralExpandableString(t *testing.T) {
	resolver := func(string) string { return "resolved" }

	testCases := []struct {
		name  string
		value string
	}{
		{name: "plain", value: "plain-value"},
		{name: "empty", value: ""},
		{name: "template syntax", value: "${ENV_VAR}"},
		{name: "double dollar", value: "pa$$word"},
		{name: "bare dollar", value: "pa$word"},
		{name: "trailing dollar", value: "value$"},
		{name: "unc path", value: `\\server\share`},
		{name: "escaped slash", value: `a\/b`},
		{name: "trailing backslash", value: `value\`},
		{name: "mixed dollar and backslash", value: `c:\dir\$$file$`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewLiteralExpandableString(tc.value)

			expanded, err := e.Envsubst(resolver)
			assert.NoError(t, err)
			assert.Equal(t, tc.value, expanded)
		})
	}
}
