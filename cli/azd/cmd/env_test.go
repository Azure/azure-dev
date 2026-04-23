// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConfigValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected any
	}{
		{
			name:     "plain_string",
			input:    "hello",
			expected: "hello",
		},
		{
			name:     "json_number",
			input:    "42",
			expected: float64(42),
		},
		{
			name:     "json_boolean_true",
			input:    "true",
			expected: true,
		},
		{
			name:     "json_boolean_false",
			input:    "false",
			expected: false,
		},
		{
			name:     "json_array",
			input:    `["a","b"]`,
			expected: []any{"a", "b"},
		},
		{
			name:     "json_object",
			input:    `{"key":"val"}`,
			expected: map[string]any{"key": "val"},
		},
		{
			name:     "null_stays_as_string",
			input:    "null",
			expected: "null",
		},
		{
			name:     "empty_string",
			input:    "",
			expected: "",
		},
		{
			name:     "string_with_spaces",
			input:    "hello world",
			expected: "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseConfigValue(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetCmdEnvHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "env"}
	result := getCmdEnvHelpDescription(cmd)
	require.Contains(t, result, "environments")
	require.Contains(t, result, "AZURE_ENV_NAME")
}

func TestGetCmdEnvConfigHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "config"}
	result := getCmdEnvConfigHelpDescription(cmd)
	require.Contains(t, result, "environment-specific configuration")
	require.Contains(t, result, "config.json")
}

func TestGetCmdEnvConfigHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "config"}
	result := getCmdEnvConfigHelpFooter(cmd)
	require.Contains(t, result, "Examples")
	require.Contains(t, result, "env config get")
	require.Contains(t, result, "env config set")
	require.Contains(t, result, "env config unset")
}

func TestNewEnvSetCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvSetCmd()
	require.Contains(t, cmd.Use, "set")
	require.NotEmpty(t, cmd.Short)
}

func TestNewEnvSelectCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvSelectCmd()
	require.Contains(t, cmd.Use, "select")
	require.NotEmpty(t, cmd.Short)
}

func TestNewEnvListCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvListCmd()
	require.Contains(t, cmd.Use, "list")
	require.NotEmpty(t, cmd.Short)
}

func TestNewEnvNewCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvNewCmd()
	require.Contains(t, cmd.Use, "new")
	require.NotEmpty(t, cmd.Short)
}

func TestNewEnvRefreshCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvRefreshCmd()
	require.Contains(t, cmd.Use, "refresh")
	require.NotEmpty(t, cmd.Short)
}

func TestNewEnvGetValuesCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvGetValuesCmd()
	require.Equal(t, "get-values", cmd.Use)
	require.NotEmpty(t, cmd.Short)
}

func TestNewEnvGetValueCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvGetValueCmd()
	require.Equal(t, "get-value <keyName>", cmd.Use)
	require.NotEmpty(t, cmd.Short)
}

func TestNewEnvConfigGetCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvConfigGetCmd()
	require.Equal(t, "get <path>", cmd.Use)
	require.NotEmpty(t, cmd.Short)
}

func TestNewEnvConfigSetCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvConfigSetCmd()
	require.Equal(t, "set <path> <value>", cmd.Use)
	require.NotEmpty(t, cmd.Short)
}

func TestNewEnvConfigUnsetCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvConfigUnsetCmd()
	require.Equal(t, "unset <path>", cmd.Use)
	require.NotEmpty(t, cmd.Short)
}
