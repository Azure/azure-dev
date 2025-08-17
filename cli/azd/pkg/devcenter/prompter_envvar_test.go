// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package devcenter

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDevCenterEnvironmentValue(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		paramType   devcentersdk.ParameterType
		expected    any
		expectError bool
	}{
		// String tests
		{
			name:      "string value",
			value:     "hello world",
			paramType: devcentersdk.ParameterTypeString,
			expected:  "hello world",
		},
		{
			name:      "empty string value",
			value:     "",
			paramType: devcentersdk.ParameterTypeString,
			expected:  "",
		},
		// Boolean tests
		{
			name:      "boolean true",
			value:     "true",
			paramType: devcentersdk.ParameterTypeBool,
			expected:  true,
		},
		{
			name:      "boolean false",
			value:     "false",
			paramType: devcentersdk.ParameterTypeBool,
			expected:  false,
		},
		{
			name:      "boolean 1",
			value:     "1",
			paramType: devcentersdk.ParameterTypeBool,
			expected:  true,
		},
		{
			name:      "boolean 0",
			value:     "0",
			paramType: devcentersdk.ParameterTypeBool,
			expected:  false,
		},
		{
			name:      "boolean yes",
			value:     "yes",
			paramType: devcentersdk.ParameterTypeBool,
			expected:  true,
		},
		{
			name:      "boolean no",
			value:     "no",
			paramType: devcentersdk.ParameterTypeBool,
			expected:  false,
		},
		{
			name:        "boolean invalid",
			value:       "maybe",
			paramType:   devcentersdk.ParameterTypeBool,
			expectError: true,
		},
		// Integer tests
		{
			name:      "integer value",
			value:     "42",
			paramType: devcentersdk.ParameterTypeInt,
			expected:  42,
		},
		{
			name:      "negative integer",
			value:     "-10",
			paramType: devcentersdk.ParameterTypeInt,
			expected:  -10,
		},
		{
			name:        "invalid integer",
			value:       "not-a-number",
			paramType:   devcentersdk.ParameterTypeInt,
			expectError: true,
		},
		{
			name:        "float as integer",
			value:       "3.14",
			paramType:   devcentersdk.ParameterTypeInt,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseDevCenterEnvironmentValue(tt.value, tt.paramType)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
