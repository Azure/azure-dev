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
	t.Parallel()
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
			t.Parallel()
			result, _ := findFirstNonFlagArg(tt.args, flagsWithValues)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindFirstNonFlagArgWithUnknownFlags(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			command, unknownFlags := findFirstNonFlagArg(tt.args, flagsWithValues)
			assert.Equal(t, tt.expectedCommand, command)
			assert.Equal(t, tt.expectedUnknownFlags, unknownFlags)
		})
	}
}

func TestExtractFlagsWithValues(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
			t.Parallel()
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

func TestParseGlobalFlags_NonInteractiveAliasAndEnvVar(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		envKey       string
		envVal       string
		wantNoPrompt bool
	}{
		{
			name:         "no flags or env",
			args:         []string{},
			wantNoPrompt: false,
		},
		{
			name:         "--no-prompt sets NoPrompt",
			args:         []string{"--no-prompt"},
			wantNoPrompt: true,
		},
		{
			name:         "--non-interactive sets NoPrompt",
			args:         []string{"--non-interactive"},
			wantNoPrompt: true,
		},
		{
			name:         "--no-prompt=false keeps NoPrompt false",
			args:         []string{"--no-prompt=false"},
			wantNoPrompt: false,
		},
		{
			name:         "AZD_NON_INTERACTIVE=true sets NoPrompt",
			args:         []string{},
			envKey:       "AZD_NON_INTERACTIVE",
			envVal:       "true",
			wantNoPrompt: true,
		},
		{
			name:         "AZD_NON_INTERACTIVE=1 sets NoPrompt",
			args:         []string{},
			envKey:       "AZD_NON_INTERACTIVE",
			envVal:       "1",
			wantNoPrompt: true,
		},
		{
			name:         "AZD_NON_INTERACTIVE=false does not set NoPrompt",
			args:         []string{},
			envKey:       "AZD_NON_INTERACTIVE",
			envVal:       "false",
			wantNoPrompt: false,
		},
		{
			name:         "AZD_NON_INTERACTIVE=0 does not set NoPrompt",
			args:         []string{},
			envKey:       "AZD_NON_INTERACTIVE",
			envVal:       "0",
			wantNoPrompt: false,
		},
		{
			name:         "explicit --no-prompt=false overrides env true",
			args:         []string{"--no-prompt=false"},
			envKey:       "AZD_NON_INTERACTIVE",
			envVal:       "true",
			wantNoPrompt: false,
		},
		{
			name:         "explicit --no-prompt overrides env false",
			args:         []string{"--no-prompt"},
			envKey:       "AZD_NON_INTERACTIVE",
			envVal:       "false",
			wantNoPrompt: true,
		},
		{
			name:         "--non-interactive overrides env false",
			args:         []string{"--non-interactive"},
			envKey:       "AZD_NON_INTERACTIVE",
			envVal:       "false",
			wantNoPrompt: true,
		},
		{
			name:         "AZD_NON_INTERACTIVE=TRUE (uppercase)",
			args:         []string{},
			envKey:       "AZD_NON_INTERACTIVE",
			envVal:       "TRUE",
			wantNoPrompt: true,
		},
		{
			name:         "both flags coexist",
			args:         []string{"--no-prompt", "--non-interactive"},
			wantNoPrompt: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear agent detection and AZD_NON_INTERACTIVE to isolate
			// from the ambient environment.
			clearAgentEnvVarsForTest(t)
			agentdetect.ResetDetection()

			// Skip if we're inside an agent and expect false
			if !tt.wantNoPrompt && tt.envKey == "" && len(tt.args) == 0 {
				if agentdetect.GetCallingAgent().Detected {
					t.Skip("skipping: agent process detected")
				}
				agentdetect.ResetDetection()
			}

			if tt.envKey != "" {
				t.Setenv(tt.envKey, tt.envVal)
			}

			opts := &internal.GlobalCommandOptions{}
			err := ParseGlobalFlags(tt.args, opts)
			require.NoError(t, err)
			assert.Equal(t, tt.wantNoPrompt, opts.NoPrompt)

			agentdetect.ResetDetection()
		})
	}

	// Standalone test: prove that AZD_NON_INTERACTIVE presence suppresses agent detection.
	// CLAUDE_CODE=1 would normally trigger NoPrompt via agent detection, but
	// AZD_NON_INTERACTIVE=false should suppress agent detection entirely.
	t.Run("AZD_NON_INTERACTIVE=false suppresses agent detection with CLAUDE_CODE set", func(t *testing.T) {
		clearAgentEnvVarsForTest(t)
		agentdetect.ResetDetection()

		t.Setenv("CLAUDE_CODE", "1")
		t.Setenv("AZD_NON_INTERACTIVE", "false")
		agentdetect.ResetDetection()

		opts := &internal.GlobalCommandOptions{}
		err := ParseGlobalFlags([]string{}, opts)
		require.NoError(t, err)
		assert.False(t, opts.NoPrompt,
			"AZD_NON_INTERACTIVE=false should suppress agent detection from setting NoPrompt")

		agentdetect.ResetDetection()
	})
}

func TestParseGlobalFlags_EnvironmentName(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		expectedEnvName string
	}{
		{
			name:            "valid env name with -e",
			args:            []string{"-e", "dev", "up"},
			expectedEnvName: "dev",
		},
		{
			name:            "valid env name with --environment",
			args:            []string{"--environment", "production", "deploy"},
			expectedEnvName: "production",
		},
		{
			name:            "valid env name with equals syntax",
			args:            []string{"--environment=staging", "deploy"},
			expectedEnvName: "staging",
		},
		{
			name:            "env name with dots and hyphens",
			args:            []string{"-e", "my-env.v2", "up"},
			expectedEnvName: "my-env.v2",
		},
		{
			name:            "empty value",
			args:            []string{"up"},
			expectedEnvName: "",
		},
		{
			name:            "env name alongside other global flags",
			args:            []string{"--debug", "-e", "myenv", "--no-prompt", "deploy"},
			expectedEnvName: "myenv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear agent detection to avoid NoPrompt side effects
			clearAgentEnvVarsForTest(t)
			agentdetect.ResetDetection()

			opts := &internal.GlobalCommandOptions{}
			err := ParseGlobalFlags(tt.args, opts)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedEnvName, opts.EnvironmentName,
				"EnvironmentName should be %q for test case: %s", tt.expectedEnvName, tt.name)

			agentdetect.ResetDetection()
		})
	}
}

func TestParseGlobalFlags_InvalidEnvironmentName(t *testing.T) {
	// Invalid environment names are silently ignored (not errors) so that
	// third-party extensions reusing -e for their own flags (e.g., URLs)
	// are not broken by azd's global flag parsing.
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "URL value",
			args: []string{"-e", "https://foo.services.ai.azure.com/api/projects/bar", "model", "custom", "create"},
		},
		{
			name: "value with colons",
			args: []string{"-e", "host:port", "model", "custom", "create"},
		},
		{
			name: "value with slashes",
			args: []string{"-e", "path/to/thing", "model", "custom", "create"},
		},
		{
			name: "value with spaces",
			args: []string{"-e", "env name with spaces"},
		},
		{
			name: "special characters",
			args: []string{"-e", "env@#$%"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAgentEnvVarsForTest(t)
			agentdetect.ResetDetection()

			opts := &internal.GlobalCommandOptions{}
			err := ParseGlobalFlags(tt.args, opts)
			require.NoError(t, err, "invalid env names should be silently ignored, not rejected")
			assert.Empty(t, opts.EnvironmentName,
				"EnvironmentName should be empty when -e value is not a valid env name")

			agentdetect.ResetDetection()
		})
	}
}

// TestParseGlobalFlags_ExtensionCompatibility verifies that extensions reusing -e for their
// own flags (e.g., azure.ai.models uses -e/--project-endpoint) work correctly alongside
// azd's global -e/--environment flag. This is a regression test for the bug that caused
// PR #7035 to be reverted (PR #7274): strict validation of -e values rejected URLs passed
// by extensions, breaking commands like `azd ai models custom create -e https://...`.
func TestParseGlobalFlags_ExtensionCompatibility(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		expectedEnvName string
		description     string
	}{
		{
			name: "azure.ai.models: -e with project endpoint URL",
			args: []string{
				"ai", "models", "custom", "create",
				"-e", "https://myaccount.services.ai.azure.com/api/projects/myproject",
			},
			expectedEnvName: "",
			description:     "URL must not be captured as env name; extension receives raw args",
		},
		{
			name: "azure.ai.models: --project-endpoint with URL",
			args: []string{
				"ai", "models", "custom", "create",
				"--project-endpoint", "https://myaccount.services.ai.azure.com/api/projects/myproject",
			},
			expectedEnvName: "",
			description:     "--project-endpoint is not a global flag, should be ignored",
		},
		{
			name: "valid env name before extension args",
			args: []string{
				"-e", "dev", "ai", "models", "custom", "create",
				"--project-endpoint", "https://endpoint.com",
			},
			expectedEnvName: "dev",
			description:     "valid -e before extension subcommand should be captured",
		},
		{
			name: "extension -e URL with other global flags",
			args: []string{
				"--debug", "ai", "models", "custom", "create",
				"-e", "https://endpoint.com", "--no-prompt",
			},
			expectedEnvName: "",
			description:     "URL via -e among global flags must not error or capture",
		},
		{
			name: "azure.ai.finetune: -e with endpoint URL",
			args: []string{
				"ai", "fine-tuning", "init",
				"-e", "https://ai-endpoint.azure.com/v1",
			},
			expectedEnvName: "",
			description:     "fine-tuning extension's -e must not be captured",
		},
		{
			name: "both --environment and -e URL: last value wins per pflag",
			args: []string{
				"--environment", "staging", "ai", "models", "custom", "create",
				"-e", "https://endpoint.com",
			},
			expectedEnvName: "",
			description: "pflag takes last -e value (the URL), " +
				"which is invalid so env name stays empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAgentEnvVarsForTest(t)
			agentdetect.ResetDetection()

			opts := &internal.GlobalCommandOptions{}
			err := ParseGlobalFlags(tt.args, opts)
			require.NoError(t, err, "ParseGlobalFlags must not error for extension args: %s", tt.description)
			assert.Equal(t, tt.expectedEnvName, opts.EnvironmentName, tt.description)

			agentdetect.ResetDetection()
		})
	}
}
