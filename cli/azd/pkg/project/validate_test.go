// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/stretchr/testify/require"
)

func TestNilDefinitionsReportAllErrors(t *testing.T) {
	yamlContent := "name: test-proj\nservices:\n  web:\n    # empty\nresources:\n  mydb:\n    # empty\n"
	projectConfig, err := Parse(context.Background(), yamlContent)
	require.Nil(t, projectConfig)
	require.Error(t, err)

	var validationErr *ConfigValidationError
	require.ErrorAs(t, err, &validationErr)

	require.Contains(t, err.Error(), "service 'web'")
	require.Contains(t, err.Error(), "resource 'mydb'")
}

func TestValidateParsedConfig(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		expected []string
	}{
		{
			name:     "NilProjectLevelHook",
			yaml:     "name: test-proj\nhooks:\n  preprovision:\n",
			expected: []string{"hook 'preprovision'"},
		},
		{
			name: "NilServiceLevelHook",
			yaml: "name: test-proj\nservices:\n  web:\n    language: python\n" +
				"    host: appservice\n    hooks:\n      predeploy:\n",
			expected: []string{"service 'web' hook 'predeploy'"},
		},
		{
			name: "NilHookEntryInArray",
			yaml: "name: test-proj\nhooks:\n  preprovision:\n    - run: echo first\n    -\n",
			expected: []string{
				"hook 'preprovision' entry 2 has an empty definition",
			},
		},
		{
			name: "MixedNilDefinitions",
			yaml: "name: test-proj\nhooks:\n  preprovision:\n" +
				"services:\n  api:\n    # empty\nresources:\n  db:\n    # empty\n",
			expected: []string{
				"service 'api'",
				"resource 'db'",
				"hook 'preprovision'",
			},
		},
		{
			name: "MultipleNilServices",
			yaml: "name: test-proj\nservices:\n  web:\n  api:\n  worker:\n",
			expected: []string{
				"service 'web'",
				"service 'api'",
				"service 'worker'",
			},
		},
		{
			name: "NilServiceSkipsHookCheck",
			yaml: "name: test-proj\nservices:\n  web:\n",
			expected: []string{
				"service 'web' has an empty definition",
			},
		},
		{
			name: "ErrorMessagesAreActionable",
			yaml: "name: test-proj\nservices:\n  web:\nresources:\n  db:\nhooks:\n  preprovision:\n",
			expected: []string{
				"expected properties such as host",
				"expected properties such as type",
				"expected properties such as run or shell",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectConfig, err := Parse(context.Background(), tt.yaml)
			require.Nil(t, projectConfig)
			require.Error(t, err)

			var validationErr *ConfigValidationError
			require.ErrorAs(t, err, &validationErr)
			require.NotEmpty(t, validationErr.Issues)

			require.Contains(t, err.Error(), "azure.yaml contains invalid configuration")
			for _, expected := range tt.expected {
				require.Contains(t, err.Error(), expected)
			}
		})
	}
}

func TestValidateParsedConfigSortedOutput(t *testing.T) {
	// Verify that errors are sorted alphabetically for deterministic output.
	// Run multiple times to guard against map iteration order variance.
	yaml := "name: test-proj\nservices:\n  zz-last:\n  mm-middle:\n  aa-first:\n  dd-fourth:\n  qq-fifth:\n"

	for range 50 {
		_, err := Parse(context.Background(), yaml)
		require.Error(t, err)

		var validationErr *ConfigValidationError
		require.ErrorAs(t, err, &validationErr)

		msg := err.Error()
		aaIdx := strings.Index(msg, "aa-first")
		ddIdx := strings.Index(msg, "dd-fourth")
		mmIdx := strings.Index(msg, "mm-middle")
		qqIdx := strings.Index(msg, "qq-fifth")
		zzIdx := strings.Index(msg, "zz-last")

		require.Greater(t, aaIdx, 0, "aa-first should be present")
		require.Greater(t, ddIdx, aaIdx, "dd-fourth should come after aa-first")
		require.Greater(t, mmIdx, ddIdx, "mm-middle should come after dd-fourth")
		require.Greater(t, qqIdx, mmIdx, "qq-fifth should come after mm-middle")
		require.Greater(t, zzIdx, qqIdx, "zz-last should come after qq-fifth")
	}
}

// TestValidateHooksNilSlice directly exercises the hookList == nil branch in validateHooks
// by constructing a ProjectConfig with a nil hook slice (as opposed to a slice containing nil entries).
// This path is reachable when a *.hooks.yaml infra module file defines a hook name with no body.
func TestValidateHooksNilSlice(t *testing.T) {
	config := &ProjectConfig{
		Name: "test-proj",
		Hooks: map[string][]*ext.HookConfig{
			"preprovision": nil,
		},
	}
	err := validateParsedConfig(config)
	require.Error(t, err)

	var validationErr *ConfigValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Len(t, validationErr.Issues, 1)
	require.Contains(t, validationErr.Issues[0], "preprovision")
	require.Contains(t, validationErr.Issues[0], "has an empty definition")
}
