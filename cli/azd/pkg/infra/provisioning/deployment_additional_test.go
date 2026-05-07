// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParameterTypeFromArmType(t *testing.T) {
	tests := []struct {
		name     string
		armType  string
		expected ParameterType
	}{
		{"String", "String", ParameterTypeString},
		{"string lowercase", "string", ParameterTypeString},
		{"secureString", "secureString", ParameterTypeString},
		{"securestring lowercase", "securestring", ParameterTypeString},
		{"Bool", "Bool", ParameterTypeBoolean},
		{"bool lowercase", "bool", ParameterTypeBoolean},
		{"Int", "Int", ParameterTypeNumber},
		{"int lowercase", "int", ParameterTypeNumber},
		{"Object", "Object", ParameterTypeObject},
		{"object lowercase", "object", ParameterTypeObject},
		{"secureObject", "secureObject", ParameterTypeObject},
		{"secureobject lowercase", "secureobject", ParameterTypeObject},
		{"Array", "Array", ParameterTypeArray},
		{"array lowercase", "array", ParameterTypeArray},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParameterTypeFromArmType(tt.armType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParameterTypeFromArmType_Panic(t *testing.T) {
	assert.Panics(t, func() {
		ParameterTypeFromArmType("unknown")
	})
}

func TestOutputParametersFromArmOutputs(t *testing.T) {
	t.Run("canonical casing from template", func(t *testing.T) {
		tplOutputs := azure.ArmTemplateOutputs{
			"AZURE_STORAGE_NAME": azure.ArmTemplateOutput{
				Type: "string",
			},
		}
		azureOutputs := map[string]azapi.AzCliDeploymentOutput{
			"azure_storage_name": {
				Type:  "string",
				Value: "mystorage",
			},
		}

		result := OutputParametersFromArmOutputs(
			tplOutputs, azureOutputs,
		)
		require.Len(t, result, 1)
		param, ok := result["AZURE_STORAGE_NAME"]
		require.True(t, ok)
		assert.Equal(t, ParameterTypeString, param.Type)
		assert.Equal(t, "mystorage", param.Value)
	})

	t.Run("uppercase fallback when not in template",
		func(t *testing.T) {
			tplOutputs := azure.ArmTemplateOutputs{}
			azureOutputs := map[string]azapi.AzCliDeploymentOutput{
				"azurE_RESOURCE_GROUP": {
					Type:  "string",
					Value: "my-rg",
				},
			}

			result := OutputParametersFromArmOutputs(
				tplOutputs, azureOutputs,
			)
			require.Len(t, result, 1)
			param, ok := result["AZURE_RESOURCE_GROUP"]
			require.True(t, ok)
			assert.Equal(t, "my-rg", param.Value)
		})

	t.Run("skips secured outputs", func(t *testing.T) {
		tplOutputs := azure.ArmTemplateOutputs{
			"secret": azure.ArmTemplateOutput{
				Type: "secureString",
			},
		}
		azureOutputs := map[string]azapi.AzCliDeploymentOutput{
			"secret": {
				Type:  "secureString",
				Value: "hidden",
			},
		}

		result := OutputParametersFromArmOutputs(
			tplOutputs, azureOutputs,
		)
		assert.Empty(t, result)
	})

	t.Run("empty inputs", func(t *testing.T) {
		result := OutputParametersFromArmOutputs(
			azure.ArmTemplateOutputs{},
			map[string]azapi.AzCliDeploymentOutput{},
		)
		assert.Empty(t, result)
	})

	t.Run("multiple outputs with mixed types",
		func(t *testing.T) {
			tplOutputs := azure.ArmTemplateOutputs{
				"myStr": azure.ArmTemplateOutput{
					Type: "string",
				},
				"myBool": azure.ArmTemplateOutput{
					Type: "bool",
				},
				"myArr": azure.ArmTemplateOutput{
					Type: "array",
				},
			}
			azureOutputs := map[string]azapi.AzCliDeploymentOutput{
				"mystr":  {Type: "string", Value: "hello"},
				"mybool": {Type: "bool", Value: true},
				"myarr": {
					Type:  "array",
					Value: []any{"a", "b"},
				},
			}

			result := OutputParametersFromArmOutputs(
				tplOutputs, azureOutputs,
			)
			require.Len(t, result, 3)
			assert.Equal(
				t, ParameterTypeString, result["myStr"].Type,
			)
			assert.Equal(
				t, ParameterTypeBoolean, result["myBool"].Type,
			)
			assert.Equal(
				t, ParameterTypeArray, result["myArr"].Type,
			)
		})
}

func TestStateMergeInto(t *testing.T) {
	t.Run("merges outputs from other into empty state",
		func(t *testing.T) {
			s := &State{}
			other := State{
				Outputs: map[string]OutputParameter{
					"key1": {
						Type: ParameterTypeString, Value: "val1",
					},
				},
				Resources: []Resource{{Id: "res-1"}},
			}

			s.MergeInto(other)
			require.Len(t, s.Outputs, 1)
			assert.Equal(t, "val1", s.Outputs["key1"].Value)
			require.Len(t, s.Resources, 1)
			assert.Equal(t, "res-1", s.Resources[0].Id)
		})

	t.Run("overwrites existing output key",
		func(t *testing.T) {
			s := &State{
				Outputs: map[string]OutputParameter{
					"key1": {
						Type: ParameterTypeString, Value: "old",
					},
				},
			}
			other := State{
				Outputs: map[string]OutputParameter{
					"key1": {
						Type: ParameterTypeString, Value: "new",
					},
				},
			}

			s.MergeInto(other)
			assert.Equal(t, "new", s.Outputs["key1"].Value)
		})

	t.Run("replaces resource by matching ID",
		func(t *testing.T) {
			s := &State{
				Resources: []Resource{{Id: "res-1"}},
			}
			other := State{
				Resources: []Resource{{Id: "res-1"}},
			}

			s.MergeInto(other)
			require.Len(t, s.Resources, 1)
		})

	t.Run("appends new resources",
		func(t *testing.T) {
			s := &State{
				Resources: []Resource{{Id: "res-1"}},
			}
			other := State{
				Resources: []Resource{{Id: "res-2"}},
			}

			s.MergeInto(other)
			require.Len(t, s.Resources, 2)
		})

	t.Run("empty other is no-op",
		func(t *testing.T) {
			s := &State{
				Outputs: map[string]OutputParameter{
					"k": {
						Type: ParameterTypeString, Value: "v",
					},
				},
				Resources: []Resource{{Id: "r1"}},
			}
			s.MergeInto(State{})
			require.Len(t, s.Outputs, 1)
			require.Len(t, s.Resources, 1)
		})
}
