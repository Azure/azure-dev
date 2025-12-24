// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ConfigOptions_SecretsAndVars(t *testing.T) {
	// Initialize the ConfigOptions instance
	projectVariables := []string{"var1", "var2"}
	projectSecrets := []string{"secret1"}

	// Define the initial variables, secrets, and environment
	initialVariables := map[string]string{
		"azdVar": "foo",
	}
	initialSecrets := map[string]string{
		"azdSecret": "foo",
	}
	env := map[string]string{
		"var1":    "foo",
		"var2":    "bar",
		"secret1": "foo",
		"secret2": "value3",
		"exraVar": "value4",
	}

	// Call the SecretsAndVars function
	variables, secrets, err := mergeProjectVariablesAndSecrets(
		projectVariables, projectSecrets, initialVariables, initialSecrets, nil, env)
	assert.NoError(t, err)

	// Assert the expected results
	expectedVariables := map[string]string{
		"azdVar": "foo",
		"var1":   "foo",
		"var2":   "bar",
	}
	expectedSecrets := map[string]string{
		"azdSecret": "foo",
		"secret1":   "foo",
	}
	assert.Equal(t, expectedVariables, variables)
	assert.Equal(t, expectedSecrets, secrets)
}

// Test_ConfigOptions_EscapedValues tests that JSON-escaped string values are preserved
// when merging project variables and secrets.
// This addresses the issue where values like `["api://..."]` need to be escaped
// to `[\"api://...\"]` when sent to remote pipelines to prevent them from being
// interpreted as JSON arrays instead of strings.
func Test_ConfigOptions_EscapedValues(t *testing.T) {
	projectVariables := []string{"AzureAd_TokenValidationParameters_ValidAudiences"}
	projectSecrets := []string{}

	initialVariables := map[string]string{}
	initialSecrets := map[string]string{}

	// This simulates a value that is read from config.json.
	// After JSON unmarshaling, the value `"[\"api://...\"]"` becomes `["api://..."]` (backslashes consumed)
	// We need to re-escape it before sending to the pipeline so it's treated as a string, not an array
	env := map[string]string{
		"AzureAd_TokenValidationParameters_ValidAudiences": "[\"api://e935a748-8b59-4c26-a59c-9bcc83f5ab57\"]",
	}

	variables, secrets, err := mergeProjectVariablesAndSecrets(
		projectVariables, projectSecrets, initialVariables, initialSecrets, nil, env)
	require.NoError(t, err)

	// After escaping, the value should have backslashes to prevent JSON parsing in the pipeline
	// The value becomes: [\"api://e935a748-8b59-4c26-a59c-9bcc83f5ab57\"]
	expectedVariables := map[string]string{
		"AzureAd_TokenValidationParameters_ValidAudiences": "[\\\"api://e935a748-8b59-4c26-a59c-9bcc83f5ab57\\\"]",
	}
	expectedSecrets := map[string]string{}

	assert.Equal(t, expectedVariables, variables)
	assert.Equal(t, expectedSecrets, secrets)
}

// Test_ConfigOptions_SimpleValues tests that simple string values are properly escaped
func Test_ConfigOptions_SimpleValues(t *testing.T) {
	projectVariables := []string{"SIMPLE_VAR", "VAR_WITH_QUOTES"}
	projectSecrets := []string{}

	initialVariables := map[string]string{}
	initialSecrets := map[string]string{}

	env := map[string]string{
		"SIMPLE_VAR":      "simple-value",
		"VAR_WITH_QUOTES": "value with \"quotes\"",
	}

	variables, secrets, err := mergeProjectVariablesAndSecrets(
		projectVariables, projectSecrets, initialVariables, initialSecrets, nil, env)
	require.NoError(t, err)

	// Simple values remain mostly the same, quotes get escaped
	expectedVariables := map[string]string{
		"SIMPLE_VAR":      "simple-value",
		"VAR_WITH_QUOTES": "value with \\\"quotes\\\"",
	}
	expectedSecrets := map[string]string{}

	assert.Equal(t, expectedVariables, variables)
	assert.Equal(t, expectedSecrets, secrets)
}

// Test_escapeValuesForPipeline tests the escape function directly
func Test_escapeValuesForPipeline(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "JSON array string",
			input:    "[\"api://guid\"]",
			expected: "[\\\"api://guid\\\"]",
		},
		{
			name:     "Simple string",
			input:    "simple",
			expected: "simple",
		},
		{
			name:     "String with quotes",
			input:    "value with \"quotes\"",
			expected: "value with \\\"quotes\\\"",
		},
		{
			name:     "String with backslashes",
			input:    "path\\to\\file",
			expected: "path\\\\to\\\\file",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]string{"test": tt.input}
			escapeValuesForPipeline(values)
			assert.Equal(t, tt.expected, values["test"])
		})
	}
}
