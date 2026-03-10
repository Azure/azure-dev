// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/stretchr/testify/require"
)

func TestGenerateBicepParam_EmptyParams(t *testing.T) {
	result := generateBicepParam("main.bicep", azure.ArmParameters{})

	expected := "using 'main.bicep'\n"
	require.Equal(t, expected, result)
}

func TestGenerateBicepParam_StringParam(t *testing.T) {
	params := azure.ArmParameters{
		"location": {Value: "eastus2"},
	}

	result := generateBicepParam("main.bicep", params)

	expected := "using 'main.bicep'\n\nparam location = 'eastus2'\n"
	require.Equal(t, expected, result)
}

func TestGenerateBicepParam_MultipleParamsSorted(t *testing.T) {
	params := azure.ArmParameters{
		"location":        {Value: "eastus2"},
		"environmentName": {Value: "dev"},
		"sku":             {Value: "Standard"},
	}

	result := generateBicepParam("infra/main.bicep", params)

	expected := "using 'infra/main.bicep'\n\n" +
		"param environmentName = 'dev'\n\n" +
		"param location = 'eastus2'\n\n" +
		"param sku = 'Standard'\n"
	require.Equal(t, expected, result)
}

func TestGenerateBicepParam_BoolParam(t *testing.T) {
	params := azure.ArmParameters{
		"enableLogging": {Value: true},
		"debugMode":     {Value: false},
	}

	result := generateBicepParam("main.bicep", params)

	expected := "using 'main.bicep'\n\n" +
		"param debugMode = false\n\n" +
		"param enableLogging = true\n"
	require.Equal(t, expected, result)
}

func TestGenerateBicepParam_NumericParam(t *testing.T) {
	params := azure.ArmParameters{
		"count": {Value: float64(3)},
	}

	result := generateBicepParam("main.bicep", params)

	expected := "using 'main.bicep'\n\nparam count = 3\n"
	require.Equal(t, expected, result)
}

func TestGenerateBicepParam_NullParam(t *testing.T) {
	params := azure.ArmParameters{
		"optionalValue": {Value: nil},
	}

	result := generateBicepParam("main.bicep", params)

	expected := "using 'main.bicep'\n\nparam optionalValue = null\n"
	require.Equal(t, expected, result)
}

func TestGenerateBicepParam_ArrayParam(t *testing.T) {
	params := azure.ArmParameters{
		"zones": {Value: []any{"1", "2", "3"}},
	}

	result := generateBicepParam("main.bicep", params)

	expected := "using 'main.bicep'\n\nparam zones = [\n  '1'\n  '2'\n  '3'\n]\n"
	require.Equal(t, expected, result)
}

func TestGenerateBicepParam_ObjectParam(t *testing.T) {
	params := azure.ArmParameters{
		"tags": {Value: map[string]any{
			"env":     "prod",
			"project": "myapp",
		}},
	}

	result := generateBicepParam("main.bicep", params)

	expected := "using 'main.bicep'\n\nparam tags = {\n  env: 'prod'\n  project: 'myapp'\n}\n"
	require.Equal(t, expected, result)
}

func TestGenerateBicepParam_SkipsKeyVaultReferences(t *testing.T) {
	params := azure.ArmParameters{
		"location": {Value: "eastus2"},
		"secret": {
			KeyVaultReference: &azure.KeyVaultParameterReference{
				KeyVault: azure.KeyVaultReference{
					ID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/myvault",
				},
				SecretName: "mySecret",
			},
		},
	}

	result := generateBicepParam("main.bicep", params)

	expected := "using 'main.bicep'\n\nparam location = 'eastus2'\n"
	require.Equal(t, expected, result)
}

func TestGenerateBicepParam_StringWithSingleQuotes(t *testing.T) {
	params := azure.ArmParameters{
		"message": {Value: "it's a test"},
	}

	result := generateBicepParam("main.bicep", params)

	expected := "using 'main.bicep'\n\nparam message = 'it''s a test'\n"
	require.Equal(t, expected, result)
}

func TestGenerateBicepParam_EmptyObject(t *testing.T) {
	params := azure.ArmParameters{
		"config": {Value: map[string]any{}},
	}

	result := generateBicepParam("main.bicep", params)

	expected := "using 'main.bicep'\n\nparam config = {}\n"
	require.Equal(t, expected, result)
}

func TestGenerateBicepParam_NilParams(t *testing.T) {
	result := generateBicepParam("main.bicep", nil)

	expected := "using 'main.bicep'\n"
	require.Equal(t, expected, result)
}
