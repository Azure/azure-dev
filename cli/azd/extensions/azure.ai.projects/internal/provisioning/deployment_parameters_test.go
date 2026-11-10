// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeploymentParameters(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input map[string]any
	}{
		{name: "nil"},
		{name: "empty", input: map[string]any{}},
		{name: "null", input: map[string]any{"input": map[string]any{"value": nil}}},
		{name: "structured values", input: map[string]any{
			"input": map[string]any{"value": map[string]any{
				"nested": []any{false, "text", json.Number("9007199254740993")},
			}},
		}},
		{name: "reference", input: map[string]any{"secret": map[string]any{
			"reference": map[string]any{
				"keyVault":   map[string]any{"id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv"},
				"secretName": "password", "secretVersion": "version",
			},
		}}},
		{name: "expression", input: map[string]any{"dynamic": map[string]any{"expression": "[variables('value')]"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := deploymentParameters(tt.input)
			require.NoError(t, err)
			expected, err := json.Marshal(tt.input)
			require.NoError(t, err)
			actual, err := json.Marshal(result)
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(actual))
			if tt.name == "structured values" {
				require.Contains(t, string(actual), "9007199254740993")
			}
		})
	}
}

func TestDeploymentParametersRejectUnsupportedFields(t *testing.T) {
	for _, input := range []map[string]any{
		{"input": map[string]any{"value": "text", "unsupported": true}},
		{"input": map[string]any{"reference": map[string]any{"unsupported": true}}},
		{"input": map[string]any{"reference": map[string]any{"keyVault": map[string]any{"unsupported": true}}}},
		{"input": nil},
		{"input": false},
	} {
		result, err := deploymentParameters(input)
		require.Error(t, err)
		require.Nil(t, result)
	}
}
