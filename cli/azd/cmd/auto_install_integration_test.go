// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/runcontext/agentdetect"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestAgentDetectionIntegration tests that agent detection works but no longer auto-enables no-prompt.
func TestAgentDetectionIntegration(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		envVars          map[string]string
		expectedNoPrompt bool
		description      string
	}{
		{
			name:             "Claude Code agent detected - no-prompt not auto-enabled",
			args:             []string{"version"},
			envVars:          map[string]string{"CLAUDE_CODE": "1"},
			expectedNoPrompt: false,
			description:      "Agent detection no longer auto-enables --no-prompt",
		},
		{
			name:             "GitHub Copilot CLI detected - no-prompt not auto-enabled",
			args:             []string{"deploy"},
			envVars:          map[string]string{"GITHUB_COPILOT_CLI": "true"},
			expectedNoPrompt: false,
			description:      "Agent detection no longer auto-enables --no-prompt",
		},
		{
			name:             "Gemini agent detected - no-prompt not auto-enabled",
			args:             []string{"init"},
			envVars:          map[string]string{"GEMINI_CLI": "1"},
			expectedNoPrompt: false,
			description:      "Agent detection no longer auto-enables --no-prompt",
		},
		{
			name:             "OpenCode agent detected - no-prompt not auto-enabled",
			args:             []string{"provision"},
			envVars:          map[string]string{"OPENCODE": "1"},
			expectedNoPrompt: false,
			description:      "Agent detection no longer auto-enables --no-prompt",
		},
		{
			name:             "User can still explicitly set --no-prompt",
			args:             []string{"--no-prompt", "up"},
			envVars:          map[string]string{"CLAUDE_CODE": "1"},
			expectedNoPrompt: true,
			description:      "Explicit --no-prompt should still work when agent is detected",
		},
		{
			name:             "Normal execution without agent detection",
			args:             []string{"version"},
			envVars:          map[string]string{},
			expectedNoPrompt: false,
			description:      "Without agent detection, prompting should remain enabled by default",
		},
		{
			name: "User agent string triggers detection but not no-prompt",
			args: []string{"up"},
			envVars: map[string]string{
				internal.AzdUserAgentEnvVar: "claude-code/1.0.0",
			},
			expectedNoPrompt: false,
			description:      "Agent detected via user agent but no-prompt not auto-enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear any ambient agent env vars to ensure test isolation
			clearAgentEnvVarsForTest(t)

			// Reset agent detection cache for each test
			agentdetect.ResetDetection()

			// Set environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			// Parse global flags as would happen in real execution
			opts := &internal.GlobalCommandOptions{}
			err := ParseGlobalFlags(tt.args, opts)
			require.NoError(t, err, "ParseGlobalFlags should not error: %s", tt.description)

			assert.Equal(t, tt.expectedNoPrompt, opts.NoPrompt,
				"NoPrompt mismatch: %s", tt.description)

			// Verify agent detection still works for telemetry even though no-prompt is not auto-set
			agent := agentdetect.GetCallingAgent()
			if len(tt.envVars) > 0 && tt.envVars["CLAUDE_CODE"] != "" ||
				tt.envVars["GITHUB_COPILOT_CLI"] != "" ||
				tt.envVars["GEMINI_CLI"] != "" ||
				tt.envVars["OPENCODE"] != "" ||
				tt.envVars[internal.AzdUserAgentEnvVar] != "" {
				assert.True(t, agent.Detected,
					"Agent should still be detected for telemetry: %s", tt.description)
			}

			// Clean up
			agentdetect.ResetDetection()
		})
	}
}

// clearAgentEnvVarsForTest clears all environment variables that could trigger agent detection.
// This ensures tests are isolated from the ambient environment.
func clearAgentEnvVarsForTest(t *testing.T) {
	envVarsToUnset := []string{
		// Claude Code
		"CLAUDE_CODE", "CLAUDE_CODE_ENTRYPOINT",
		// GitHub Copilot CLI
		"GITHUB_COPILOT_CLI", "GH_COPILOT",
		// Gemini CLI
		"GEMINI_CLI", "GEMINI_CLI_NO_RELAUNCH",
		// OpenCode
		"OPENCODE",
		// User agent
		internal.AzdUserAgentEnvVar,
	}

	for _, envVar := range envVarsToUnset {
		if _, exists := os.LookupEnv(envVar); exists {
			t.Setenv(envVar, "")
			os.Unsetenv(envVar)
		}
	}
}
