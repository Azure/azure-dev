// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapToStringSlice(t *testing.T) {
	// Test case 1: Empty map
	m1 := make(map[string]string)
	expected1 := []string(nil)
	result1 := mapToStringSlice(m1, ":")
	assert.ElementsMatch(t, expected1, result1)

	// Test case 2: Map with values
	m2 := map[string]string{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}
	expected2 := []string{"key1:value1", "key2:value2", "key3:value3"}
	result2 := mapToStringSlice(m2, ":")
	assert.ElementsMatch(t, expected2, result2)

	// Test case 3: Map with empty values
	m3 := map[string]string{
		"key1": "",
		"key2": "",
		"key3": "",
	}
	expected3 := []string{"key1", "key2", "key3"}
	result3 := mapToStringSlice(m3, ":")
	assert.ElementsMatch(t, expected3, result3)
}

func TestEvaluateArgsWithConfig(t *testing.T) {
	envParamName := "param4"
	envParamKey := strings.TrimSuffix(scaffold.EnvFormat(envParamName)[2:], "}")
	envParamExpected := "valueFromEnv"
	t.Setenv(envParamKey, envParamExpected)

	manifest := apphost.Manifest{
		Resources: map[string]*apphost.Resource{
			"param1": {
				Type:  "parameter.v0",
				Value: "value1",
			},
			"param2": {
				Type:  "parameter.v0",
				Value: "value2",
			},
			"param3": {
				Type:  "parameter.v0",
				Value: "{param3.inputs.iParam}",
				Inputs: map[string]apphost.Input{
					"iParam": {
						Type: "string",
					},
				},
			},
			envParamName: {
				Type:  "parameter.v0",
				Value: fmt.Sprintf("{%s.inputs.foo}", envParamName),
				Inputs: map[string]apphost.Input{
					"foo": {
						Type: "string",
					},
				},
			},
		},
	}

	args := map[string]string{
		"arg1": "{param1.value}",
		"arg2": "{param2.value}",
		"arg3": "constant",
		"arg4": "{param3.value}",
		"arg5": "{param4.value}",
	}

	expected := map[string]string{
		// evaluation completed
		"arg1": "value1",
		"arg2": "value2",
		// constant value
		"arg3": "constant",
		// evaluation delayed until building container
		"arg4": "{infra.parameters.param3}",
		// evaluation from environment variable
		"arg5": envParamExpected,
	}

	result, err := evaluateArgsWithConfig(manifest, args)
	require.NoError(t, err)
	require.ElementsMatch(t, mapToStringSlice(expected, ","), mapToStringSlice(result, ","))
}

func TestBuildArgsArrayAndEnv(t *testing.T) {
	manifest := apphost.Manifest{
		Resources: map[string]*apphost.Resource{
			"param1": {
				Type:  "parameter.v0",
				Value: "value1",
			},
			"param2": {
				Type:  "parameter.v0",
				Value: "value2",
			},
		},
	}

	bArgs := map[string]apphost.ContainerV1BuildSecrets{
		"arg1": {
			Type:  "env",
			Value: to.Ptr("{param1.value}"),
		},
		"arg2": {
			Type:   "file",
			Source: to.Ptr("/path/to/secret"),
		},
	}

	expectedArgs := []string{
		"id=arg1",
		"id=arg2,src=/path/to/secret",
	}

	expectedEnv := []string{
		"arg1=value1",
	}

	args, env, err := buildArgsArrayAndEnv(manifest, bArgs)
	require.NoError(t, err)
	assert.ElementsMatch(t, expectedArgs, args)
	assert.ElementsMatch(t, expectedEnv, env)
}

func TestMapToExpandableStringSliceWithHyphenConversion(t *testing.T) {
	// Test that build args with infra.parameters. prefix get hyphens converted to underscores
	buildArgs := map[string]string{
		"ARG1": "value1",
		"ARG2": "{infra.parameters.param-name}",
		"ARG3": "{infra.parameters.another-param-name}",
		"ARG4": "{some.other.param-name}",             // Should NOT be converted (no infra.parameters. prefix)
		"ARG5": "regular-value-with-hyphens",          // Should NOT be converted (not a parameter reference)
		"ARG6": "{infra.parameters.param_underscore}", // Should remain unchanged (already has underscore)
		"ARG7": "{infra.parameters.param-name-with-suffix}",
		"ARG8": "",
	}

	result := handleBuildArgsNames(mapToExpandableStringSlice(buildArgs, "="))

	// Convert result to strings for easier comparison using identity mapping
	resultStrings := make([]string, len(result))
	for i, item := range result {
		resultStrings[i] = item.MustEnvsubst(func(name string) string { return "${" + name + "}" })
	}

	expected := []string{
		"ARG1=value1",
		"ARG2={infra.parameters.param_name}",             // Hyphen converted to underscore
		"ARG3={infra.parameters.another_param_name}",     // Hyphens converted to underscores
		"ARG4={some.other.param-name}",                   // NOT converted (wrong prefix)
		"ARG5=regular-value-with-hyphens",                // NOT converted (not a parameter reference)
		"ARG6={infra.parameters.param_underscore}",       // Unchanged (already has underscore)
		"ARG7={infra.parameters.param_name_with_suffix}", // All hyphens converted
		"ARG8", // Empty value
	}

	assert.ElementsMatch(t, expected, resultStrings)
}

func TestEvaluateArgsWithConfigHyphenHandling(t *testing.T) {
	// Test integration with evaluateArgsWithConfig to ensure hyphens in parameter names
	// get converted to underscores in infra.parameters references
	manifest := apphost.Manifest{
		Resources: map[string]*apphost.Resource{
			"param-with-hyphens": {
				Type:  "parameter.v0",
				Value: "{param-with-hyphens.inputs.inputParam}",
				Inputs: map[string]apphost.Input{
					"inputParam": {
						Type: "string",
					},
				},
			},
		},
	}

	args := map[string]string{
		"BUILD_ARG1": "{param-with-hyphens.value}",
		"BUILD_ARG2": "constant-value",
	}

	// Evaluate args first (this should produce {infra.parameters.param-with-hyphens})
	evaluatedArgs, err := evaluateArgsWithConfig(manifest, args)
	require.NoError(t, err)

	// Now apply hyphen conversion when creating expandable strings
	result := handleBuildArgsNames(mapToExpandableStringSlice(evaluatedArgs, "="))

	// Convert to strings for verification
	resultStrings := make([]string, len(result))
	for i, item := range result {
		resultStrings[i] = item.MustEnvsubst(func(name string) string { return "${" + name + "}" })
	}

	expected := []string{
		"BUILD_ARG1={infra.parameters.param_with_hyphens}", // Hyphen converted to underscore
		"BUILD_ARG2=constant-value",                        // Unchanged (not a parameter reference)
	}

	assert.ElementsMatch(t, expected, resultStrings)
}

func TestConvertHyphensInInfraParameters(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "infra.parameters with hyphens",
			input:    "{infra.parameters.my-param-name}",
			expected: "{infra.parameters.my_param_name}",
		},
		{
			name:     "infra.parameters with underscores",
			input:    "{infra.parameters.my_param_name}",
			expected: "{infra.parameters.my_param_name}",
		},
		{
			name:     "non-infra parameter reference",
			input:    "{some.other.param-name}",
			expected: "{some.other.param-name}",
		},
		{
			name:     "regular string with hyphens",
			input:    "regular-string-with-hyphens",
			expected: "regular-string-with-hyphens",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "malformed parameter reference",
			input:    "{infra.parameters.param-name",
			expected: "{infra.parameters.param-name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertHyphensInInfraParameters(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
