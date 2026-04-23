// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ParseEnvironmentVariables
// ---------------------------------------------------------------------------

func TestParseEnvironmentVariables(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected map[string]string
	}{
		{
			name:     "empty input",
			input:    []string{},
			expected: map[string]string{},
		},
		{
			name:     "nil input",
			input:    nil,
			expected: map[string]string{},
		},
		{
			name:     "simple key value",
			input:    []string{"FOO=bar"},
			expected: map[string]string{"FOO": "bar"},
		},
		{
			name:     "quoted value",
			input:    []string{`DB_HOST="localhost"`},
			expected: map[string]string{"DB_HOST": "localhost"},
		},
		{
			name:     "multiple entries",
			input:    []string{"A=1", "B=2", "C=3"},
			expected: map[string]string{"A": "1", "B": "2", "C": "3"},
		},
		{
			name:     "value with equals sign",
			input:    []string{"CONNECTION=host=localhost;port=5432"},
			expected: map[string]string{"CONNECTION": "host=localhost;port=5432"},
		},
		{
			name:     "comment lines are skipped",
			input:    []string{"# this is a comment", "KEY=value"},
			expected: map[string]string{"KEY": "value"},
		},
		{
			name:     "empty lines are skipped",
			input:    []string{"A=1", "", "  ", "B=2"},
			expected: map[string]string{"A": "1", "B": "2"},
		},
		{
			name:     "empty value",
			input:    []string{"EMPTY="},
			expected: map[string]string{"EMPTY": ""},
		},
		{
			name:     "quoted empty value",
			input:    []string{`EMPTY=""`},
			expected: map[string]string{"EMPTY": ""},
		},
		{
			name:     "line without equals is skipped",
			input:    []string{"NOEQUALSSIGN", "VALID=yes"},
			expected: map[string]string{"VALID": "yes"},
		},
		{
			name:     "whitespace around key and value",
			input:    []string{"  KEY  =  value  "},
			expected: map[string]string{"KEY": "value"},
		},
		{
			name:     "single quote not stripped",
			input:    []string{"KEY='value'"},
			expected: map[string]string{"KEY": "'value'"},
		},
		{
			name:     "empty key before equals is skipped",
			input:    []string{"=value", "VALID=yes"},
			expected: map[string]string{"VALID": "yes"},
		},
		{
			name:     "carriage return in values (Windows line endings)",
			input:    []string{"FOO=bar\r", "BAZ=qux\r"},
			expected: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name: "mixed scenario",
			input: []string{
				"# Environment variables",
				"",
				"AZURE_LOCATION=eastus2",
				`AZURE_RESOURCE_GROUP="my-rg"`,
				"CONNECTION_STRING=Server=tcp:myserver.database.windows.net;Database=mydb",
				"# end of file",
			},
			expected: map[string]string{
				"AZURE_LOCATION":       "eastus2",
				"AZURE_RESOURCE_GROUP": "my-rg",
				"CONNECTION_STRING":    "Server=tcp:myserver.database.windows.net;Database=mydb",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseEnvironmentVariables(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}
