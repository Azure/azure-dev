// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissingInputsError_Error_SingleInput(t *testing.T) {
	err := &MissingInputsError{
		Inputs: []MissingInput{
			{
				Name:          "location",
				Type:          "string",
				EnvVarNames:   []string{"AZURE_LOCATION"},
				ConfigKey:     "infra.parameters.location",
				AllowedValues: []string{"eastus", "westus"},
				Description:   "The Azure region for resources",
				Secure:        false,
			},
		},
	}

	assert.Equal(t, "missing required inputs", err.Error())

	output := err.ToString("")

	assert.Contains(t, output, "Provision cannot continue (interactive prompts disabled)")
	assert.Contains(t, output, "1 required input is missing")
	assert.Contains(t, output, "• location")
	assert.Contains(t, output, "Environment variable: AZURE_LOCATION")
	assert.Contains(t, output, "Environment configuration key: infra.parameters.location")
	assert.Contains(t, output, "Type: string")
	assert.Contains(t, output, "Allowed values: eastus, westus")
	assert.Contains(t, output, "Description: The Azure region for resources")
	assert.Contains(t, output, "You can resolve these by:")
	assert.Contains(t, output, "azd env set")
	assert.Contains(t, output, "azd env config set")
	assert.Contains(t, output, "azd provision")
}

func TestMissingInputsError_Error_MultipleInputs(t *testing.T) {
	err := &MissingInputsError{
		Inputs: []MissingInput{
			{
				Name:        "location",
				Type:        "string",
				EnvVarNames: []string{"AZURE_LOCATION"},
				ConfigKey:   "infra.parameters.location",
			},
			{
				Name:        "apiKey",
				Type:        "string",
				EnvVarNames: []string{"API_KEY"},
				ConfigKey:   "infra.parameters.apiKey",
				Secure:      true,
			},
		},
	}

	output := err.ToString("")

	assert.Contains(t, output, "2 required inputs are missing")
	assert.Contains(t, output, "• location")
	assert.Contains(t, output, "• apiKey")
	assert.Contains(t, output, "Environment variable: AZURE_LOCATION")
	assert.Contains(t, output, "Environment variable: API_KEY")
}

func TestMissingInputsError_Error_NoEnvVars(t *testing.T) {
	err := &MissingInputsError{
		Inputs: []MissingInput{
			{
				Name:      "param1",
				Type:      "string",
				ConfigKey: "infra.parameters.param1",
			},
		},
	}

	output := err.ToString("")

	assert.NotContains(t, output, "1) Setting environment variables")
	assert.Contains(t, output, "1) Setting environment configuration")
	assert.NotContains(t, output, "2) Setting environment configuration")
}

func TestConstraintDetails(t *testing.T) {
	tests := []struct {
		name     string
		input    MissingInput
		expected []string
	}{
		{
			name: "AllowedValues",
			input: MissingInput{
				AllowedValues: []string{"a", "b", "c"},
			},
			expected: []string{"Allowed values: a, b, c"},
		},
		{
			name: "MinAndMaxLength",
			input: MissingInput{
				Constraints: &InputConstraints{MinLength: new(5), MaxLength: new(20)},
			},
			expected: []string{"Length: 5–20"},
		},
		{
			name: "MinLengthOnly",
			input: MissingInput{
				Constraints: &InputConstraints{MinLength: new(3)},
			},
			expected: []string{"Min length: 3"},
		},
		{
			name: "MaxLengthOnly",
			input: MissingInput{
				Constraints: &InputConstraints{MaxLength: new(100)},
			},
			expected: []string{"Max length: 100"},
		},
		{
			name: "MinAndMaxValue",
			input: MissingInput{
				Constraints: &InputConstraints{MinValue: new(1), MaxValue: new(100)},
			},
			expected: []string{"Value: 1–100"},
		},
		{
			name: "Secure",
			input: MissingInput{
				Secure: true,
			},
			expected: []string{"Secure: true"},
		},
		{
			name:     "NoConstraints",
			input:    MissingInput{},
			expected: nil,
		},
		{
			name: "AllConstraints",
			input: MissingInput{
				AllowedValues: []string{"x"},
				Secure:        true,
				Constraints:   &InputConstraints{MinLength: new(1), MaxLength: new(50)},
			},
			expected: []string{"Allowed values: x", "Length: 1–50", "Secure: true"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := constraintDetails(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestBuildConstraints_SecureType(t *testing.T) {
	param := azure.ArmTemplateParameterDefinition{
		Type: "securestring",
	}

	prompts := []struct {
		key   string
		param azure.ArmTemplateParameterDefinition
	}{{key: "secret", param: param}}

	p := &BicepProvider{}
	result := p.buildMissingInputsError(prompts, nil)

	require.Len(t, result.Inputs, 1)
	assert.Equal(t, "string", result.Inputs[0].Type)
	assert.True(t, result.Inputs[0].Secure)
}

func TestBuildConstraints_SecureObjectType(t *testing.T) {
	param := azure.ArmTemplateParameterDefinition{
		Type: "secureobject",
	}

	prompts := []struct {
		key   string
		param azure.ArmTemplateParameterDefinition
	}{{key: "obj", param: param}}

	p := &BicepProvider{}
	result := p.buildMissingInputsError(prompts, nil)

	require.Len(t, result.Inputs, 1)
	assert.Equal(t, "object", result.Inputs[0].Type)
	assert.True(t, result.Inputs[0].Secure)
}

func TestMissingInputsError_Error_WithAllDetails(t *testing.T) {
	err := &MissingInputsError{
		Inputs: []MissingInput{
			{
				Name:          "resourceGroupLocation",
				Type:          "string",
				EnvVarNames:   []string{"AZURE_LOCATION", "AZURE_DEPLOYMENT_REGION"},
				ConfigKey:     "infra.parameters.resourceGroupLocation",
				AllowedValues: []string{"eastus", "westus", "centralus"},
				Description:   "Location for all resources",
				Constraints:   &InputConstraints{MinLength: new(1), MaxLength: new(50)},
			},
			{
				Name:        "storageAccountKey",
				Type:        "string",
				EnvVarNames: []string{"STORAGE_KEY"},
				ConfigKey:   "infra.parameters.storageAccountKey",
				Description: "Storage account access key",
				Secure:      true,
			},
		},
	}

	output := err.ToString("")

	assert.Contains(t, output, "Provision cannot continue (interactive prompts disabled)")
	assert.Contains(t, output, "2 required inputs are missing")

	assert.Contains(t, output, "• resourceGroupLocation")
	assert.Contains(t, output, "Environment variable: AZURE_LOCATION, AZURE_DEPLOYMENT_REGION")
	assert.Contains(t, output, "Environment configuration key: infra.parameters.resourceGroupLocation")
	assert.Contains(t, output, "Allowed values: eastus, westus, centralus")
	assert.Contains(t, output, "Length: 1–50")

	assert.Contains(t, output, "• storageAccountKey")
	assert.Contains(t, output, "Environment variable: STORAGE_KEY")
	assert.Contains(t, output, "Secure: true")

	assert.Contains(t, output, "You can resolve these by:")
	assert.Contains(t, output, "1) Setting environment variables")
	assert.Contains(t, output, "2) Setting environment configuration")
	assert.Contains(t, output, "Then re-run:")
	assert.Contains(t, output, "azd provision")
}

func TestMissingInputsError_MarshalJSON(t *testing.T) {
	err := &MissingInputsError{
		Inputs: []MissingInput{
			{
				Name:        "location",
				Type:        "string",
				EnvVarNames: []string{"AZURE_LOCATION"},
				ConfigKey:   "infra.parameters.location",
				Description: "The Azure region",
				Constraints: &InputConstraints{MinLength: new(1)},
			},
		},
	}

	jsonData, marshalErr := err.MarshalJSON()
	require.NoError(t, marshalErr)

	var result struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Type   string         `json:"type"`
			Inputs []MissingInput `json:"inputs"`
		} `json:"details"`
	}
	require.NoError(t, json.Unmarshal(jsonData, &result))

	assert.Equal(t, "provisionMissingInputs", result.Code)
	assert.Equal(t, "Provision cannot continue (interactive prompts disabled)", result.Message)
	assert.Equal(t, "provisionMissingInputs", result.Details.Type)
	require.Len(t, result.Details.Inputs, 1)

	input := result.Details.Inputs[0]
	assert.Equal(t, "location", input.Name)
	assert.Equal(t, "string", input.Type)
	assert.Equal(t, "infra.parameters.location", input.ConfigKey)
	assert.Equal(t, []string{"AZURE_LOCATION"}, input.EnvVarNames)
	assert.Equal(t, "The Azure region", input.Description)
	require.NotNil(t, input.Constraints)
	require.NotNil(t, input.Constraints.MinLength)
	assert.Equal(t, 1, *input.Constraints.MinLength)
	assert.Nil(t, input.Constraints.MaxLength)
}

func TestMissingInputsError_MarshalJSON_OmitsEmptyConstraints(t *testing.T) {
	err := &MissingInputsError{
		Inputs: []MissingInput{
			{
				Name:      "flag",
				Type:      "bool",
				ConfigKey: "infra.parameters.flag",
			},
		},
	}

	jsonData, marshalErr := err.MarshalJSON()
	require.NoError(t, marshalErr)

	// Verify that constraints key is omitted entirely when nil
	raw := string(jsonData)
	assert.NotContains(t, raw, "constraints")
	assert.NotContains(t, raw, "minLength")
	assert.NotContains(t, raw, "maxLength")
	assert.NotContains(t, raw, "minValue")
	assert.NotContains(t, raw, "maxValue")
}
