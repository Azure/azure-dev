// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPromptRequiredError_Error(t *testing.T) {
	err := &PromptRequiredError{Message: DefaultPromptRequiredMessage}

	require.Equal(t, "prompt required", err.Error())
}

func TestPromptRequiredError_ToString_UsesGenericEnvExample(t *testing.T) {
	err := &PromptRequiredError{
		Message: DefaultPromptRequiredMessage,
		Inputs: []RequiredInput{
			{
				Name: "subscription",
				Sources: []InputSource{
					{
						Kind: InputSourceFlag,
						Name: "--subscription",
					},
					{
						Kind: InputSourceEnvironment,
						Name: "AZURE_SUBSCRIPTION_ID",
					},
				},
			},
		},
	}

	output := err.ToString("")
	require.Contains(t, output, DefaultPromptRequiredMessage)
	require.Contains(t, output, "1 required input is missing")
	require.Contains(t, output, "• subscription")
	require.Contains(t, output, "Flag: --subscription")
	require.Contains(t, output, "Environment: AZURE_SUBSCRIPTION_ID")
	require.Contains(t, output, "Example:")
	require.Contains(t, output, "azd env set <ENV_VAR_NAME> <value>")
}

func TestPromptRequiredError_ToString_UsesConcreteEnvExamples(t *testing.T) {
	err := &PromptRequiredError{
		Message: DefaultPromptRequiredMessage,
		Inputs: []RequiredInput{
			{
				Name: "location",
				Sources: []InputSource{
					{
						Kind:         InputSourceEnvironment,
						Name:         "AZURE_LOCATION",
						ExampleValue: "westus2",
					},
				},
			},
			{
				Name: "resource group",
				Sources: []InputSource{
					{
						Kind:         InputSourceEnvironment,
						Name:         "AZURE_RESOURCE_GROUP",
						ExampleValue: "my-app-rg",
					},
				},
			},
		},
	}

	output := err.ToString("")
	require.Contains(t, output, "Examples:")
	require.Contains(t, output, "azd env set AZURE_LOCATION westus2")
	require.Contains(t, output, "azd env set AZURE_RESOURCE_GROUP my-app-rg")
	require.NotContains(t, output, "azd env set <ENV_VAR_NAME> <value>")
}

func TestPromptRequiredError_ToString_UsesDescription(t *testing.T) {
	err := &PromptRequiredError{
		Message: DefaultPromptRequiredMessage,
		Inputs: []RequiredInput{
			{
				Name:        "OpenAI account",
				Description: "OpenAI account must be selected to continue.",
			},
		},
	}

	expectedOutput := strings.Join([]string{
		strings.Repeat("─", 62),
		DefaultPromptRequiredMessage,
		strings.Repeat("─", 62),
		"",
		"1 required input is missing.",
		"",
		"Missing required inputs:",
		"",
		"• OpenAI account",
		"    Description: OpenAI account must be selected to continue.",
		"",
		"",
	}, "\n")

	require.Equal(t, expectedOutput, err.ToString(""))
}

func TestPromptRequiredError_ToString_UsesPromptMessageWhenInputsMissing(t *testing.T) {
	err := &PromptRequiredError{
		PromptMessage: "Enter name:",
	}

	expectedOutput := strings.Join([]string{
		"The following prompt requires user input:",
		"",
		"  ? Enter name:",
		"",
		"This prompt cannot be answered non-interactively. To proceed, run this command in interactive mode.",
		"",
	}, "\n")

	require.Equal(t, expectedOutput, err.ToString(""))
}

func TestPromptRequiredError_MarshalJSON(t *testing.T) {
	err := &PromptRequiredError{
		Message: DefaultPromptRequiredMessage,
		Inputs: []RequiredInput{
			{
				Name: "subscription",
				Sources: []InputSource{
					{
						Kind: InputSourceEnvironment,
						Name: "AZURE_SUBSCRIPTION_ID",
					},
					{
						Kind:         InputSourceFlag,
						Name:         "--subscription",
						ExampleValue: "<subscription-id>",
					},
				},
			},
		},
	}

	data, marshalErr := err.MarshalJSON()
	require.NoError(t, marshalErr)

	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Type   string          `json:"type"`
			Inputs []RequiredInput `json:"inputs"`
		} `json:"details"`
	}
	require.NoError(t, json.Unmarshal(data, &payload))

	require.Equal(t, promptRequiredCode, payload.Code)
	require.Equal(t, DefaultPromptRequiredMessage, payload.Message)
	require.Equal(t, promptRequiredMissingInputsType, payload.Details.Type)
	require.Equal(t, err.Inputs, payload.Details.Inputs)
}

func TestPromptRequiredError_MarshalJSON_UsesDefaultMessageWhenEmpty(t *testing.T) {
	err := &PromptRequiredError{
		Inputs: []RequiredInput{
			{Name: "subscription"},
		},
	}

	data, marshalErr := err.MarshalJSON()
	require.NoError(t, marshalErr)

	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(data, &payload))
	require.Equal(t, DefaultPromptRequiredMessage, payload.Message)
}

func TestPromptRequiredError_MarshalJSON_IncludesPromptMessageWhenInputsMissing(t *testing.T) {
	err := &PromptRequiredError{
		PromptMessage: "Enter name:",
	}

	data, marshalErr := err.MarshalJSON()
	require.NoError(t, marshalErr)

	var payload struct {
		Message string `json:"message"`
		Details struct {
			PromptMessage string `json:"promptMessage"`
		} `json:"details"`
	}
	require.NoError(t, json.Unmarshal(data, &payload))

	require.Equal(t, DefaultPromptRequiredMessage, payload.Message)
	require.Equal(t, "Enter name:", payload.Details.PromptMessage)
}
