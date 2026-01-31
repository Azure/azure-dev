// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyQuery_EmptyQuery(t *testing.T) {
	data := map[string]interface{}{
		"name": "test",
	}

	result, err := ApplyQuery(data, "")
	require.NoError(t, err)
	require.Equal(t, data, result)
}

func TestApplyQuery_PropertyAccess(t *testing.T) {
	data := map[string]interface{}{
		"name":   "myenv",
		"status": "active",
	}

	result, err := ApplyQuery(data, "name")
	require.NoError(t, err)
	require.Equal(t, "myenv", result)
}

func TestApplyQuery_NestedPropertyAccess(t *testing.T) {
	data := map[string]interface{}{
		"config": map[string]interface{}{
			"setting": "value",
		},
	}

	result, err := ApplyQuery(data, "config.setting")
	require.NoError(t, err)
	require.Equal(t, "value", result)
}

func TestApplyQuery_ArrayFiltering(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{"name": "env1", "status": "active"},
		map[string]interface{}{"name": "env2", "status": "inactive"},
		map[string]interface{}{"name": "env3", "status": "active"},
	}

	result, err := ApplyQuery(data, "[?status=='active']")
	require.NoError(t, err)

	filtered, ok := result.([]interface{})
	require.True(t, ok)
	require.Len(t, filtered, 2)
}

func TestApplyQuery_ArrayProjection(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{"name": "env1", "status": "active"},
		map[string]interface{}{"name": "env2", "status": "inactive"},
	}

	result, err := ApplyQuery(data, "[].name")
	require.NoError(t, err)

	names, ok := result.([]interface{})
	require.True(t, ok)
	require.Equal(t, []interface{}{"env1", "env2"}, names)
}

func TestApplyQuery_ObjectProjection(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{"name": "env1", "status": "active"},
	}

	result, err := ApplyQuery(data, "[].{n: name, s: status}")
	require.NoError(t, err)

	projected, ok := result.([]interface{})
	require.True(t, ok)
	require.Len(t, projected, 1)

	item, ok := projected[0].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "env1", item["n"])
	require.Equal(t, "active", item["s"])
}

func TestApplyQuery_EmptyResult(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{"name": "env1", "status": "active"},
	}

	result, err := ApplyQuery(data, "[?status=='nonexistent']")
	require.NoError(t, err)

	filtered, ok := result.([]interface{})
	require.True(t, ok)
	require.Len(t, filtered, 0)
}

func TestApplyQuery_InvalidQuery(t *testing.T) {
	data := map[string]interface{}{"name": "test"}

	_, err := ApplyQuery(data, "[invalid query syntax")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid JMESPath query")
	require.Contains(t, err.Error(), "https://jmespath.org")
}

func TestApplyQuery_ArrayIndexing(t *testing.T) {
	data := []interface{}{"first", "second", "third"}

	result, err := ApplyQuery(data, "[0]")
	require.NoError(t, err)
	require.Equal(t, "first", result)
}

func TestApplyQuery_NullResult(t *testing.T) {
	data := map[string]interface{}{"name": "test"}

	result, err := ApplyQuery(data, "nonexistent")
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestJsonFormatterWithQuery(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{"name": "env1", "status": "active"},
		map[string]interface{}{"name": "env2", "status": "inactive"},
	}

	formatter := &JsonFormatter{Query: "[].name"}
	buffer := &bytes.Buffer{}

	err := formatter.Format(data, buffer, nil)
	require.NoError(t, err)

	expected := `[
  "env1",
  "env2"
]
`
	require.Equal(t, expected, buffer.String())
}

func TestJsonFormatterWithQuery_InvalidQuery(t *testing.T) {
	data := map[string]interface{}{"name": "test"}

	formatter := &JsonFormatter{Query: "[invalid"}
	buffer := &bytes.Buffer{}

	err := formatter.Format(data, buffer, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "https://jmespath.org")
}

func TestJsonFormatterWithQuery_FilterAndProject(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{"name": "env1", "isDefault": true},
		map[string]interface{}{"name": "env2", "isDefault": false},
	}

	formatter := &JsonFormatter{Query: "[?isDefault].name"}
	buffer := &bytes.Buffer{}

	err := formatter.Format(data, buffer, nil)
	require.NoError(t, err)

	// Should only contain "env1" since that's the default
	require.Contains(t, buffer.String(), "env1")
	require.NotContains(t, buffer.String(), "env2")
}

func TestApplyQuery_DocumentationHintInError(t *testing.T) {
	testCases := []struct {
		name  string
		query string
	}{
		{"unclosed bracket", "["},
		{"invalid filter", "[?"},
		{"bad syntax", "..."},
	}

	data := map[string]interface{}{"name": "test"}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ApplyQuery(data, tc.query)
			require.Error(t, err)
			// Verify documentation hint is present
			require.True(t,
				strings.Contains(err.Error(), "jmespath.org"),
				"Error should contain documentation hint: %s", err.Error())
		})
	}
}
