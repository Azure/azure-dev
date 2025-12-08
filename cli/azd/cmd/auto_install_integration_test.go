// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// TestExecuteWithAutoInstallIntegration tests the integration between
// extractFlagsWithValues and findFirstNonFlagArg in the context of
// the auto-install feature.
func TestExecuteWithAutoInstallIntegration(t *testing.T) {
	// Save original args
	originalArgs := os.Args

	// Test cases that would have failed before the fix
	testCases := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "output flag with demo command",
			args:     []string{"azd", "--output", "json", "demo"},
			expected: "demo",
		},
		{
			name:     "cwd flag with init command",
			args:     []string{"azd", "--cwd", "/project", "init"},
			expected: "init",
		},
		{
			name:     "mixed flags",
			args:     []string{"azd", "--debug", "--output", "table", "--no-prompt", "deploy"},
			expected: "deploy",
		},
		{
			name:     "short form flags",
			args:     []string{"azd", "-o", "json", "-C", "/path", "up"},
			expected: "up",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set test args
			os.Args = tc.args

			// Create a test root command to extract flags from
			rootCmd := &cobra.Command{Use: "azd"}

			// Add the flags that azd actually uses
			rootCmd.PersistentFlags().StringP("output", "o", "", "Output format")
			rootCmd.PersistentFlags().StringP("cwd", "C", "", "Working directory")
			rootCmd.PersistentFlags().Bool("debug", false, "Debug mode")
			rootCmd.PersistentFlags().Bool("no-prompt", false, "No prompting")

			// Extract flags and test our parsing
			flagsWithValues := extractFlagsWithValues(rootCmd)
			result, _ := findFirstNonFlagArg(os.Args[1:], flagsWithValues)

			assert.Equal(t, tc.expected, result,
				"Failed to correctly identify command in args: %v", tc.args)
		})
	}

	// Restore original args
	os.Args = originalArgs
}
