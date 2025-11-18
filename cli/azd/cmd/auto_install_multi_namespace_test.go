// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/azure/azure-dev/pkg/extensions"
	"github.com/stretchr/testify/assert"
)

func TestCheckForMatchingExtensionsLogic(t *testing.T) {
	// Test the core logic without needing to mock the extension manager
	// We'll create a simple function that mimics the matching logic

	testExtensions := []*extensions.ExtensionMetadata{
		{
			Id:          "extension1",
			Namespace:   "demo",
			DisplayName: "Demo Extension",
			Description: "Simple demo extension",
		},
		{
			Id:          "extension2",
			Namespace:   "vhvb.demo",
			DisplayName: "VHVB Demo Extension",
			Description: "VHVB namespace demo extension",
		},
		{
			Id:          "extension3",
			Namespace:   "vhvb.demo.advanced",
			DisplayName: "Advanced VHVB Demo",
			Description: "Advanced demo with longer namespace",
		},
		{
			Id:          "extension4",
			Namespace:   "other.namespace",
			DisplayName: "Other Extension",
			Description: "Different namespace pattern",
		},
	}

	// Helper function that mimics checkForMatchingExtensions logic
	checkMatches := func(
		args []string, availableExtensions []*extensions.ExtensionMetadata) []*extensions.ExtensionMetadata {
		if len(args) == 0 {
			return nil
		}

		var matchingExtensions []*extensions.ExtensionMetadata

		// Generate all possible namespace combinations from the command arguments
		for i := 1; i <= len(args); i++ {
			candidateNamespace := strings.Join(args[:i], ".")

			// Check if any extension has this exact namespace
			for _, ext := range availableExtensions {
				if ext.Namespace == candidateNamespace {
					matchingExtensions = append(matchingExtensions, ext)
				}
			}
		}

		return matchingExtensions
	}

	tests := []struct {
		name            string
		args            []string
		expectedMatches []string // Extension IDs that should match
	}{
		{
			name:            "single word matches single extension",
			args:            []string{"demo"},
			expectedMatches: []string{"extension1"},
		},
		{
			name:            "two words matches nested namespace",
			args:            []string{"vhvb", "demo"},
			expectedMatches: []string{"extension2"},
		},
		{
			name:            "three words matches deep namespace",
			args:            []string{"vhvb", "demo", "advanced"},
			expectedMatches: []string{"extension2", "extension3"}, // Both vhvb.demo and vhvb.demo.advanced should match
		},
		{
			name:            "multiple matches for progressive namespaces",
			args:            []string{"vhvb", "demo", "advanced", "extra"},
			expectedMatches: []string{"extension2", "extension3"}, // Both vhvb.demo and vhvb.demo.advanced should match
		},
		{
			name:            "no matches for unknown namespace",
			args:            []string{"unknown", "command"},
			expectedMatches: []string{},
		},
		{
			name:            "empty args returns no matches",
			args:            []string{},
			expectedMatches: []string{},
		},
		{
			name:            "partial namespace without full match",
			args:            []string{"vhvb"},
			expectedMatches: []string{}, // No extension with namespace "vhvb" exists
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Execute function
			matches := checkMatches(tt.args, testExtensions)

			// Verify results
			assert.Equal(t, len(tt.expectedMatches), len(matches), "Number of matches should be correct")

			// Check that the right extensions were matched
			matchedIds := make([]string, len(matches))
			for i, match := range matches {
				matchedIds[i] = match.Id
			}

			for _, expectedId := range tt.expectedMatches {
				assert.Contains(t, matchedIds, expectedId, "Expected extension %s to be in matches", expectedId)
			}
		})
	}
}
