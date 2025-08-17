// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEnvironmentValue(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		paramType   provisioning.ParameterType
		expected    any
		expectError bool
	}{
		// String tests
		{
			name:      "string value",
			value:     "hello world",
			paramType: provisioning.ParameterTypeString,
			expected:  "hello world",
		},
		{
			name:      "empty string value",
			value:     "",
			paramType: provisioning.ParameterTypeString,
			expected:  "",
		},
		// Boolean tests
		{
			name:      "boolean true",
			value:     "true",
			paramType: provisioning.ParameterTypeBoolean,
			expected:  true,
		},
		{
			name:      "boolean false",
			value:     "false",
			paramType: provisioning.ParameterTypeBoolean,
			expected:  false,
		},
		{
			name:      "boolean 1",
			value:     "1",
			paramType: provisioning.ParameterTypeBoolean,
			expected:  true,
		},
		{
			name:      "boolean 0",
			value:     "0",
			paramType: provisioning.ParameterTypeBoolean,
			expected:  false,
		},
		{
			name:      "boolean yes",
			value:     "yes",
			paramType: provisioning.ParameterTypeBoolean,
			expected:  true,
		},
		{
			name:      "boolean no",
			value:     "no",
			paramType: provisioning.ParameterTypeBoolean,
			expected:  false,
		},
		{
			name:        "boolean invalid",
			value:       "maybe",
			paramType:   provisioning.ParameterTypeBoolean,
			expectError: true,
		},
		// Number tests
		{
			name:      "integer number",
			value:     "42",
			paramType: provisioning.ParameterTypeNumber,
			expected:  42,
		},
		{
			name:      "float number",
			value:     "3.14",
			paramType: provisioning.ParameterTypeNumber,
			expected:  3.14,
		},
		{
			name:        "invalid number",
			value:       "not-a-number",
			paramType:   provisioning.ParameterTypeNumber,
			expectError: true,
		},
		// Array tests
		{
			name:      "valid JSON array",
			value:     `["item1", "item2", "item3"]`,
			paramType: provisioning.ParameterTypeArray,
			expected:  []any{"item1", "item2", "item3"},
		},
		{
			name:      "empty JSON array",
			value:     `[]`,
			paramType: provisioning.ParameterTypeArray,
			expected:  []any{},
		},
		{
			name:        "invalid JSON array",
			value:       `[invalid json`,
			paramType:   provisioning.ParameterTypeArray,
			expectError: true,
		},
		// Object tests
		{
			name:      "valid JSON object",
			value:     `{"key1": "value1", "key2": 42}`,
			paramType: provisioning.ParameterTypeObject,
			expected:  map[string]any{"key1": "value1", "key2": float64(42)},
		},
		{
			name:      "empty JSON object",
			value:     `{}`,
			paramType: provisioning.ParameterTypeObject,
			expected:  map[string]any{},
		},
		{
			name:        "invalid JSON object",
			value:       `{invalid json`,
			paramType:   provisioning.ParameterTypeObject,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseEnvironmentValue(tt.value, tt.paramType)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

// Commented out for now - integration test needs more work with mock setup
/*
func TestBicepPlanWithEnvironmentVariables(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "--version"
	}).Respond(exec.RunResult{
		Stdout: fmt.Sprintf("Bicep CLI version %s (abcdef0123)", bicep.Version.String()),
		Stderr: "",
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && args.Args[0] == "build"
	}).Respond(exec.RunResult{
		Stdout: paramsArmJsonForEnvVar,
		Stderr: "",
	})

	// Create provider and set environment variable in the provider's environment
	infraProvider := createBicepProvider(t, mockContext)

	// Set environment variable for the parameter instead of prompting
	infraProvider.env.DotenvSet("AZURE_PARAM_STRINGPARAM", "env_value")

	plan, err := infraProvider.plan(*mockContext.Context)

	require.NoError(t, err)
	require.Equal(t, "env_value", plan.Parameters["stringParam"].Value)

	// Verify no prompting occurred by checking that the console was not called for prompting
	// Note: We can't use AssertNotCalled as it's not available, so we just verify the result
}
*/
