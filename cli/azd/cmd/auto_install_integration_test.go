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

// TestAgentDetectionIntegration tests the full agent detection integration flow.
func TestAgentDetectionIntegration(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		envVars          map[string]string
		expectedNoPrompt bool
		description      string
	}{
		{
			name:             "Claude Code agent enables no-prompt automatically",
			args:             []string{"version"},
			envVars:          map[string]string{"CLAUDE_CODE": "1"},
			expectedNoPrompt: true,
			description:      "When running under Claude Code, --no-prompt should be auto-enabled",
		},
		{
			name:             "Cursor agent enables no-prompt automatically",
			args:             []string{"up"},
			envVars:          map[string]string{"CURSOR_EDITOR": "1"},
			expectedNoPrompt: true,
			description:      "When running under Cursor, --no-prompt should be auto-enabled",
		},
		{
			name:             "GitHub Copilot CLI enables no-prompt automatically",
			args:             []string{"deploy"},
			envVars:          map[string]string{"GITHUB_COPILOT_CLI": "true"},
			expectedNoPrompt: true,
			description:      "When running under GitHub Copilot CLI, --no-prompt should be auto-enabled",
		},
		{
			name:             "Windsurf agent enables no-prompt automatically",
			args:             []string{"init"},
			envVars:          map[string]string{"WINDSURF_EDITOR": "1"},
			expectedNoPrompt: true,
			description:      "When running under Windsurf, --no-prompt should be auto-enabled",
		},
		{
			name:             "Aider agent enables no-prompt automatically",
			args:             []string{"provision"},
			envVars:          map[string]string{"AIDER_MODEL": "gpt-4"},
			expectedNoPrompt: true,
			description:      "When running under Aider, --no-prompt should be auto-enabled",
		},
		{
			name:             "User can override agent detection with --no-prompt=false",
			args:             []string{"--no-prompt=false", "up"},
			envVars:          map[string]string{"CLAUDE_CODE": "1"},
			expectedNoPrompt: false,
			description:      "Explicit --no-prompt=false should override agent detection",
		},
		{
			name:             "Normal execution without agent detection",
			args:             []string{"version"},
			envVars:          map[string]string{},
			expectedNoPrompt: false,
			description:      "Without agent detection, prompting should remain enabled by default",
		},
		{
			name: "User agent string triggers detection",
			args: []string{"up"},
			envVars: map[string]string{
				internal.AzdUserAgentEnvVar: "claude-code/1.0.0",
			},
			expectedNoPrompt: true,
			description:      "AZURE_DEV_USER_AGENT containing agent identifier should trigger detection",
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

			// Verify agent detection status matches expectation
			agent := agentdetect.GetCallingAgent()
			if tt.expectedNoPrompt && len(tt.envVars) > 0 && !containsNoPromptFalse(tt.args) {
				assert.True(t, agent.Detected,
					"Agent should be detected when agent env vars are set: %s", tt.description)
			}

			// Clean up
			agentdetect.ResetDetection()
		})
	}
}

// containsNoPromptFalse checks if args contain --no-prompt=false
func containsNoPromptFalse(args []string) bool {
	for _, arg := range args {
		if arg == "--no-prompt=false" {
			return true
		}
	}
	return false
}

// clearAgentEnvVarsForTest clears all environment variables that could trigger agent detection.
// This ensures tests are isolated from the ambient environment.
func clearAgentEnvVarsForTest(t *testing.T) {
	envVarsToUnset := []string{
		// Claude Code
		"CLAUDE_CODE", "CLAUDE_CODE_ENTRYPOINT",
		// GitHub Copilot CLI
		"GITHUB_COPILOT_CLI", "GH_COPILOT",
		// OpenAI Codex
		"OPENAI_CODEX", "CODEX_CLI",
		// Cursor
		"CURSOR_EDITOR", "CURSOR_SESSION_ID", "CURSOR_TRACE_ID",
		// Windsurf
		"WINDSURF_EDITOR", "WINDSURF_SESSION",
		// Zed
		"ZED_TERM",
		// Aider
		"AIDER_MODEL", "AIDER_CHAT_LANGUAGE",
		// Continue
		"CONTINUE_GLOBAL_DIR", "CONTINUE_DEVELOPMENT",
		// Amazon Q
		"AMAZON_Q_DEVELOPER", "AWS_Q_DEVELOPER", "KIRO_CLI",
		// Cline
		"CLINE_MCP",
		// Tabnine
		"TABNINE_CONFIG",
		// Cody
		"CODY_CONFIG",
		// Gemini CLI
		"GEMINI_CLI", "GEMINI_CLI_NO_RELAUNCH", "GEMINI_CODE_ASSIST",
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
