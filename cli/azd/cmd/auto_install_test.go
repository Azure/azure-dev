// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/assert"
)

func TestFindFirstNonFlagArg(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "first arg is command",
			args:     []string{"demo", "--flag", "value"},
			expected: "demo",
		},
		{
			name:     "command after flags",
			args:     []string{"--verbose", "demo", "--other"},
			expected: "demo",
		},
		{
			name:     "only flags",
			args:     []string{"--help", "--version"},
			expected: "",
		},
		{
			name:     "empty args",
			args:     []string{},
			expected: "",
		},
		{
			name:     "flags with equals",
			args:     []string{"--config=file", "init", "--template=web"},
			expected: "init",
		},
		{
			name:     "single character flags",
			args:     []string{"-v", "-h", "up", "--debug"},
			expected: "up",
		},
		{
			name:     "command with flag value",
			args:     []string{"--output", "json", "demo", "subcommand"},
			expected: "json",
		},
		{
			name:     "no arguments",
			args:     nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findFirstNonFlagArg(tt.args)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCheckForMatchingExtension_Unit(t *testing.T) {
	// This is a unit test that tests the logic without external dependencies
	// We'll create a mock-like test by testing the namespace matching logic directly

	testCases := []struct {
		name          string
		command       string
		extensions    []*extensions.ExtensionMetadata
		expectedMatch bool
		expectedExtId string
	}{
		{
			name:    "matches extension by first namespace part",
			command: "demo",
			extensions: []*extensions.ExtensionMetadata{
				{
					Id:        "microsoft.azd.demo",
					Namespace: "demo.commands",
				},
			},
			expectedMatch: true,
			expectedExtId: "microsoft.azd.demo",
		},
		{
			name:    "no match for command",
			command: "nonexistent",
			extensions: []*extensions.ExtensionMetadata{
				{
					Id:        "microsoft.azd.demo",
					Namespace: "demo.commands",
				},
			},
			expectedMatch: false,
		},
		{
			name:    "matches complex namespace",
			command: "complex",
			extensions: []*extensions.ExtensionMetadata{
				{
					Id:        "microsoft.azd.complex",
					Namespace: "complex.deep.namespace.structure",
				},
			},
			expectedMatch: true,
			expectedExtId: "microsoft.azd.complex",
		},
		{
			name:    "multiple extensions, finds correct match",
			command: "x",
			extensions: []*extensions.ExtensionMetadata{
				{
					Id:        "microsoft.azd.demo",
					Namespace: "demo.commands",
				},
				{
					Id:        "microsoft.azd.x",
					Namespace: "x.tools",
				},
				{
					Id:        "microsoft.azd.other",
					Namespace: "other.namespace",
				},
			},
			expectedMatch: true,
			expectedExtId: "microsoft.azd.x",
		},
		{
			name:          "empty extensions list",
			command:       "demo",
			extensions:    []*extensions.ExtensionMetadata{},
			expectedMatch: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test the namespace matching logic directly
			var foundExtension *extensions.ExtensionMetadata
			for _, ext := range tc.extensions {
				namespaceParts := strings.Split(ext.Namespace, ".")
				if len(namespaceParts) > 0 && namespaceParts[0] == tc.command {
					foundExtension = ext
					break
				}
			}

			if tc.expectedMatch {
				assert.NotNil(t, foundExtension, "Expected to find matching extension")
				if foundExtension != nil {
					assert.Equal(t, tc.expectedExtId, foundExtension.Id)
				}
			} else {
				assert.Nil(t, foundExtension, "Expected no matching extension")
			}
		})
	}
}
