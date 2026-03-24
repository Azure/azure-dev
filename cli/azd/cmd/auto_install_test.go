// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/runcontext/agentdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindFirstNonFlagArg(t *testing.T) {
	// Mock flags that take values for testing
	flagsWithValues := map[string]bool{
		"--output":         true,
		"-o":               true,
		"--cwd":            true,
		"-C":               true,
		"--trace-log-file": true,
		"--trace-log-url":  true,
		"--config":         true, // Additional test flag
	}

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
			name:     "command after boolean flags",
			args:     []string{"--debug", "--no-prompt", "demo"},
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
			args:     []string{"--output=json", "demo", "--template=web"},
			expected: "demo",
		},
		{
			name:     "single character boolean flags",
			args:     []string{"-v", "-h", "up", "--debug"},
			expected: "up",
		},
		{
			name:     "command with output flag value (the original problem)",
			args:     []string{"--output", "json", "demo", "subcommand"},
			expected: "demo", // Fixed: should be "demo", not "json"
		},
		{
			name:     "command with cwd flag value",
			args:     []string{"--cwd", "/some/path", "demo"},
			expected: "demo",
		},
		{
			name:     "command with short output flag",
			args:     []string{"-o", "table", "init"},
			expected: "init",
		},
		{
			name:     "command with short cwd flag",
			args:     []string{"-C", "/path", "up"},
			expected: "up",
		},
		{
			name:     "mixed flags with values and boolean",
			args:     []string{"--debug", "--output", "json", "--no-prompt", "deploy"},
			expected: "deploy",
		},
		{
			name:     "no arguments",
			args:     nil,
			expected: "",
		},
		{
			name:     "trace log flags",
			args:     []string{"--trace-log-file", "debug.log", "monitor"},
			expected: "monitor",
		},
		{
			name:     "complex real world example",
			args:     []string{"--debug", "--cwd", "/project", "--output", "json", "demo", "--template", "minimal"},
			expected: "demo",
		},
		{
			name:     "test with custom config flag",
			args:     []string{"--config", "myconfig.yaml", "deploy"},
			expected: "deploy",
		},
		{
			name:     "unknown flag that appears boolean",
			args:     []string{"--unknown", "command"},
			expected: "command",
		},
		{
			name:     "unknown flag that takes value - PROBLEMATIC CASE",
			args:     []string{"--unknown-flag", "some-value", "command"},
			expected: "command", // Currently returns "some-value" - this is the problem!
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := findFirstNonFlagArg(tt.args, flagsWithValues)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindFirstNonFlagArgWithUnknownFlags(t *testing.T) {
	flagsWithValues := map[string]bool{
		"--output": true,
		"-o":       true,
		"--cwd":    true,
		"-C":       true,
	}

	tests := []struct {
		name                 string
		args                 []string
		expectedCommand      string
		expectedUnknownFlags []string
	}{
		{
			name:                 "no unknown flags",
			args:                 []string{"--output", "json", "deploy"},
			expectedCommand:      "deploy",
			expectedUnknownFlags: []string{},
		},
		{
			name:                 "single unknown flag before command",
			args:                 []string{"--unknown", "command"},
			expectedCommand:      "command",
			expectedUnknownFlags: []string{"--unknown"},
		},
		{
			name:                 "unknown flag that takes value",
			args:                 []string{"--unknown-flag", "some-value", "command"},
			expectedCommand:      "command",
			expectedUnknownFlags: []string{"--unknown-flag"},
		},
		{
			name:                 "multiple unknown flags",
			args:                 []string{"--flag1", "--flag2", "value", "command"},
			expectedCommand:      "command",
			expectedUnknownFlags: []string{"--flag1", "--flag2"},
		},
		{
			name:                 "mixed known and unknown flags",
			args:                 []string{"--output", "json", "--unknown", "deploy"},
			expectedCommand:      "deploy",
			expectedUnknownFlags: []string{"--unknown"},
		},
		{
			name:                 "unknown flag with equals",
			args:                 []string{"--unknown=value", "command"},
			expectedCommand:      "command",
			expectedUnknownFlags: []string{"--unknown"},
		},
		{
			name:                 "only unknown flags, no command",
			args:                 []string{"--unknown1", "--unknown2"},
			expectedCommand:      "",
			expectedUnknownFlags: []string{"--unknown1", "--unknown2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, unknownFlags := findFirstNonFlagArg(tt.args, flagsWithValues)
			assert.Equal(t, tt.expectedCommand, command)
			assert.Equal(t, tt.expectedUnknownFlags, unknownFlags)
		})
	}
}

func TestExtractFlagsWithValues(t *testing.T) {
	// Create a test command with various flag types
	cmd := &cobra.Command{
		Use: "test",
	}

	// Add flags that take values
	cmd.Flags().StringP("output", "o", "", "Output format")
	cmd.PersistentFlags().StringP("cwd", "C", "", "Working directory")
	cmd.Flags().String("config", "", "Config file")

	// Add boolean flags (should not be included)
	cmd.Flags().Bool("debug", false, "Debug mode")
	cmd.PersistentFlags().Bool("no-prompt", false, "No prompting")

	// Add flags with other value types
	cmd.Flags().Int("port", 8080, "Port number")
	cmd.Flags().StringSlice("tags", []string{}, "Tags")

	// Extract flags
	flagsWithValues := extractFlagsWithValues(cmd)

	// Test that flags with values are included
	assert.True(t, flagsWithValues["--output"], "Should include --output flag")
	assert.True(t, flagsWithValues["-o"], "Should include -o shorthand")
	assert.True(t, flagsWithValues["--cwd"], "Should include --cwd persistent flag")
	assert.True(t, flagsWithValues["-C"], "Should include -C shorthand")
	assert.True(t, flagsWithValues["--config"], "Should include --config flag")
	assert.True(t, flagsWithValues["--port"], "Should include --port flag (int type)")
	assert.True(t, flagsWithValues["--tags"], "Should include --tags flag (slice type)")

	// Test that boolean flags are NOT included
	assert.False(t, flagsWithValues["--debug"], "Should not include boolean --debug flag")
	assert.False(t, flagsWithValues["--no-prompt"], "Should not include boolean --no-prompt flag")

	// Test non-existent flags
	assert.False(t, flagsWithValues["--nonexistent"], "Should not include non-existent flags")
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

func TestParseGlobalFlags_AgentDetection(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		envVars          map[string]string
		expectedNoPrompt bool
	}{
		{
			name:             "no agent detected, no flag",
			args:             []string{"up"},
			envVars:          map[string]string{},
			expectedNoPrompt: false,
		},
		{
			name:             "agent detected via env var, no flag",
			args:             []string{"up"},
			envVars:          map[string]string{"CLAUDE_CODE": "1"},
			expectedNoPrompt: true,
		},
		{
			name:             "agent detected but --no-prompt=false explicitly set",
			args:             []string{"--no-prompt=false", "up"},
			envVars:          map[string]string{"CLAUDE_CODE": "1"},
			expectedNoPrompt: false,
		},
		{
			name:             "agent detected but --no-prompt explicitly set true",
			args:             []string{"--no-prompt", "up"},
			envVars:          map[string]string{"GEMINI_CLI": "1"},
			expectedNoPrompt: true,
		},
		{
			name:             "no agent, --no-prompt explicitly set",
			args:             []string{"--no-prompt", "deploy"},
			envVars:          map[string]string{},
			expectedNoPrompt: true,
		},
		{
			name:             "Gemini agent detected",
			args:             []string{"init"},
			envVars:          map[string]string{"GEMINI_CLI": "1"},
			expectedNoPrompt: true,
		},
		{
			name:             "GitHub Copilot CLI agent detected",
			args:             []string{"deploy"},
			envVars:          map[string]string{"GITHUB_COPILOT_CLI": "true"},
			expectedNoPrompt: true,
		},
		{
			name:             "OpenCode agent detected",
			args:             []string{"provision"},
			envVars:          map[string]string{"OPENCODE": "1"},
			expectedNoPrompt: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear any ambient agent env vars to ensure test isolation
			clearAgentEnvVarsForTest(t)

			// Reset agent detection cache
			agentdetect.ResetDetection()

			// If the test expects no agent but we're inside an agent process, skip it.
			if !tt.expectedNoPrompt && len(tt.envVars) == 0 {
				if agentdetect.GetCallingAgent().Detected {
					t.Skip("skipping: parent process detection found an agent")
				}
				agentdetect.ResetDetection()
			}

			// Set up env vars for this test
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			opts := &internal.GlobalCommandOptions{}
			err := ParseGlobalFlags(tt.args, opts)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedNoPrompt, opts.NoPrompt,
				"NoPrompt should be %v for test case: %s", tt.expectedNoPrompt, tt.name)

			// Clean up for next test
			agentdetect.ResetDetection()
		})
	}
}
