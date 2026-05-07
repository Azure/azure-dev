// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type testNodeConfig struct {
	PackageManager string `json:"packageManager"`
}

type testPythonConfig struct {
	VirtualEnvName string `json:"virtualEnvName"`
}

type testDotnetConfig struct {
	Configuration string `json:"configuration"`
	Framework     string `json:"framework"`
}

type testNestedConfig struct {
	Name    string `json:"name"`
	Count   int    `json:"count"`
	Verbose bool   `json:"verbose"`
}

func TestUnmarshalHookConfig(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "NilConfig",
			run: func(t *testing.T) {
				result, err := UnmarshalHookConfig[testNodeConfig](nil)
				require.NoError(t, err)
				require.Equal(t, testNodeConfig{}, result)
			},
		},
		{
			name: "EmptyConfig",
			run: func(t *testing.T) {
				result, err := UnmarshalHookConfig[testNodeConfig](map[string]any{})
				require.NoError(t, err)
				require.Equal(t, testNodeConfig{}, result)
			},
		},
		{
			name: "ValidStringField",
			run: func(t *testing.T) {
				config := map[string]any{
					"packageManager": "pnpm",
				}
				result, err := UnmarshalHookConfig[testNodeConfig](config)
				require.NoError(t, err)
				require.Equal(t, "pnpm", result.PackageManager)
			},
		},
		{
			name: "ValidPythonConfig",
			run: func(t *testing.T) {
				config := map[string]any{
					"virtualEnvName": ".myenv",
				}
				result, err := UnmarshalHookConfig[testPythonConfig](config)
				require.NoError(t, err)
				require.Equal(t, ".myenv", result.VirtualEnvName)
			},
		},
		{
			name: "ValidDotnetConfigMultipleFields",
			run: func(t *testing.T) {
				config := map[string]any{
					"configuration": "Release",
					"framework":     "net8.0",
				}
				result, err := UnmarshalHookConfig[testDotnetConfig](config)
				require.NoError(t, err)
				require.Equal(t, "Release", result.Configuration)
				require.Equal(t, "net8.0", result.Framework)
			},
		},
		{
			name: "ValidNestedTypes",
			run: func(t *testing.T) {
				config := map[string]any{
					"name":    "test-hook",
					"count":   42,
					"verbose": true,
				}
				result, err := UnmarshalHookConfig[testNestedConfig](config)
				require.NoError(t, err)
				require.Equal(t, "test-hook", result.Name)
				require.Equal(t, 42, result.Count)
				require.True(t, result.Verbose)
			},
		},
		{
			name: "TypeMismatchReturnsError",
			run: func(t *testing.T) {
				config := map[string]any{
					"packageManager": 123,
				}
				// JSON numbers unmarshal into strings as an error
				_, err := UnmarshalHookConfig[testNodeConfig](config)
				require.Error(t, err)
				require.Contains(t, err.Error(), "unmarshalling hook config")
			},
		},
		{
			name: "UnknownKeysIgnored",
			run: func(t *testing.T) {
				config := map[string]any{
					"packageManager": "npm",
					"unknownField":   "some-value",
					"anotherExtra":   42,
				}
				result, err := UnmarshalHookConfig[testNodeConfig](config)
				require.NoError(t, err)
				require.Equal(t, "npm", result.PackageManager)
			},
		},
		{
			name: "PartialConfigUsesZeroDefaults",
			run: func(t *testing.T) {
				config := map[string]any{
					"configuration": "Debug",
				}
				result, err := UnmarshalHookConfig[testDotnetConfig](config)
				require.NoError(t, err)
				require.Equal(t, "Debug", result.Configuration)
				require.Empty(t, result.Framework)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}
