// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInjectParameterValues_BracesNoSpaces(t *testing.T) {
	t.Parallel()

	template := "model: {{deploymentName}}"
	params := ParameterValues{"deploymentName": "gpt-4o"}

	result, err := injectParameterValues(template, params)
	require.NoError(t, err)
	require.Equal(t, "model: gpt-4o", string(result))
}

func TestInjectParameterValues_BracesWithSpaces(t *testing.T) {
	t.Parallel()

	template := "model: {{ deploymentName }}"
	params := ParameterValues{"deploymentName": "gpt-4o"}

	result, err := injectParameterValues(template, params)
	require.NoError(t, err)
	require.Equal(t, "model: gpt-4o", string(result))
}

func TestInjectParameterValues_BothForms(t *testing.T) {
	t.Parallel()

	template := "first: {{name}}\nsecond: {{ name }}"
	params := ParameterValues{"name": "my-agent"}

	result, err := injectParameterValues(template, params)
	require.NoError(t, err)
	require.Equal(t, "first: my-agent\nsecond: my-agent", string(result))
}

func TestInjectParameterValues_NonStringValues(t *testing.T) {
	t.Parallel()

	template := "count: {{replicas}}\nenabled: {{debug}}"
	params := ParameterValues{
		"replicas": 3,
		"debug":    true,
	}

	result, err := injectParameterValues(template, params)
	require.NoError(t, err)
	require.Contains(t, string(result), "count: 3")
	require.Contains(t, string(result), "enabled: true")
}

func TestInjectParameterValues_MultipleParams(t *testing.T) {
	t.Parallel()

	template := "model: {{model}}\nregion: {{region}}"
	params := ParameterValues{
		"model":  "gpt-4o",
		"region": "eastus",
	}

	result, err := injectParameterValues(template, params)
	require.NoError(t, err)
	require.Contains(t, string(result), "model: gpt-4o")
	require.Contains(t, string(result), "region: eastus")
}

func TestInjectParameterValues_NoParams(t *testing.T) {
	t.Parallel()

	template := "name: my-agent"
	params := ParameterValues{}

	result, err := injectParameterValues(template, params)
	require.NoError(t, err)
	require.Equal(t, "name: my-agent", string(result))
}

func TestInjectParameterValues_EmptyTemplate(t *testing.T) {
	t.Parallel()

	result, err := injectParameterValues("", ParameterValues{"x": "y"})
	require.NoError(t, err)
	require.Equal(t, "", string(result))
}

func TestInjectParameterValuesIntoManifest_RoundTrip(t *testing.T) {
	t.Parallel()

	manifest := &AgentManifest{
		Name: "test-agent",
		Template: ContainerAgent{
			AgentDefinition: AgentDefinition{
				Kind: AgentKindHosted,
				Name: "test-agent",
			},
		},
	}

	// Even with no params to inject, the round-trip should succeed
	result, err := InjectParameterValuesIntoManifest(manifest, ParameterValues{})
	require.NoError(t, err)
	require.Equal(t, "test-agent", result.Name)
}

func TestPromptForYamlParameterValues_NoPrompt(t *testing.T) {
	const (
		sensitiveName = "AZD_TEST_MANIFEST_INPUT_9570"
		enumName      = "AZD_TEST_MANIFEST_ENUM_9570"
		requiredName  = "AZD_TEST_MANIFEST_REQUIRED_9570"
	)

	t.Setenv(sensitiveName, "test-value")
	t.Setenv(enumName, "second")

	defaultValue := any("default-value")
	enumValues := []any{"first", "second"}
	values, err := promptForYamlParameterValues(
		t.Context(),
		PropertySchema{Properties: []Property{
			{Name: sensitiveName, Required: new(true), Secret: new(true)},
			{Name: enumName, Required: new(true), EnumValues: &enumValues},
			{Name: "optional_with_default", Default: &defaultValue},
			{Name: "optional_without_default"},
		}},
		nil,
		true,
	)

	require.NoError(t, err)
	require.Equal(t, "test-value", values[sensitiveName])
	require.Equal(t, "second", values[enumName])
	require.Equal(t, "default-value", values["optional_with_default"])
	require.Equal(t, "", values["optional_without_default"])

	_, err = promptForYamlParameterValues(
		t.Context(),
		PropertySchema{Properties: []Property{{Name: requiredName, Required: new(true)}}},
		nil,
		true,
	)
	require.EqualError(
		t,
		err,
		"parameter 'AZD_TEST_MANIFEST_REQUIRED_9570' is required in no-prompt mode; "+
			"set environment variable AZD_TEST_MANIFEST_REQUIRED_9570",
	)
}

func TestPromptForYamlParameterValues_RejectsInvalidEnvironmentEnum(t *testing.T) {
	const name = "AZD_TEST_MANIFEST_INVALID_ENUM_9570"
	t.Setenv(name, "invalid")

	enumValues := []any{"first", "second"}
	_, err := promptForYamlParameterValues(
		t.Context(),
		PropertySchema{Properties: []Property{{
			Name:       name,
			EnumValues: &enumValues,
		}}},
		nil,
		true,
	)

	require.EqualError(
		t,
		err,
		"environment variable AZD_TEST_MANIFEST_INVALID_ENUM_9570 has invalid value \"invalid\" "+
			"for parameter 'AZD_TEST_MANIFEST_INVALID_ENUM_9570'; expected one of [first second]",
	)
}
