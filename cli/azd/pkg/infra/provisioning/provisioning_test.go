// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEnvRefreshResultFromState(t *testing.T) {
	t.Run("all parameter types mapped", func(t *testing.T) {
		state := &State{
			Outputs: map[string]OutputParameter{
				"str":  {Type: ParameterTypeString, Value: "hi"},
				"num":  {Type: ParameterTypeNumber, Value: 42},
				"bool": {Type: ParameterTypeBoolean, Value: true},
				"obj": {
					Type:  ParameterTypeObject,
					Value: map[string]any{"k": "v"},
				},
				"arr": {
					Type:  ParameterTypeArray,
					Value: []any{1, 2},
				},
			},
			Resources: []Resource{
				{Id: "res-1"},
				{Id: "res-2"},
			},
		}

		result := NewEnvRefreshResultFromState(state)

		require.Len(t, result.Outputs, 5)
		assert.Equal(
			t,
			contracts.EnvRefreshOutputTypeString,
			result.Outputs["str"].Type,
		)
		assert.Equal(
			t,
			contracts.EnvRefreshOutputTypeNumber,
			result.Outputs["num"].Type,
		)
		assert.Equal(
			t,
			contracts.EnvRefreshOutputTypeBoolean,
			result.Outputs["bool"].Type,
		)
		assert.Equal(
			t,
			contracts.EnvRefreshOutputTypeObject,
			result.Outputs["obj"].Type,
		)
		assert.Equal(
			t,
			contracts.EnvRefreshOutputTypeArray,
			result.Outputs["arr"].Type,
		)

		require.Len(t, result.Resources, 2)
		assert.Equal(t, "res-1", result.Resources[0].Id)
		assert.Equal(t, "res-2", result.Resources[1].Id)
	})

	t.Run("empty state", func(t *testing.T) {
		state := &State{
			Outputs:   map[string]OutputParameter{},
			Resources: []Resource{},
		}

		result := NewEnvRefreshResultFromState(state)
		assert.Empty(t, result.Outputs)
		assert.Empty(t, result.Resources)
	})

	t.Run("output values preserved", func(t *testing.T) {
		state := &State{
			Outputs: map[string]OutputParameter{
				"key": {
					Type:  ParameterTypeString,
					Value: "test-value",
				},
			},
			Resources: []Resource{},
		}

		result := NewEnvRefreshResultFromState(state)
		assert.Equal(
			t, "test-value", result.Outputs["key"].Value,
		)
	})
}

func TestParseProvider(t *testing.T) {
	tests := []struct {
		name     string
		kind     ProviderKind
		expected ProviderKind
	}{
		{
			name:     "empty defaults to NotSpecified",
			kind:     NotSpecified,
			expected: NotSpecified,
		},
		{
			name:     "bicep",
			kind:     Bicep,
			expected: Bicep,
		},
		{
			name:     "terraform",
			kind:     Terraform,
			expected: Terraform,
		},
		{
			name:     "test",
			kind:     Test,
			expected: Test,
		},
		{
			name:     "custom provider is accepted",
			kind:     ProviderKind("custom-ext"),
			expected: ProviderKind("custom-ext"),
		},
		{
			name:     "pulumi is accepted",
			kind:     Pulumi,
			expected: Pulumi,
		},
		{
			name:     "arm is accepted",
			kind:     Arm,
			expected: Arm,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseProvider(tt.kind)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
