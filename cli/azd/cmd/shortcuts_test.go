// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// createTestCobraCommand creates a mock Cobra command tree for testing
func createTestCobraCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "azd",
	}

	// Add main commands
	addCmd := &cobra.Command{Use: "add"}
	authCmd := &cobra.Command{Use: "auth"}
	buildCmd := &cobra.Command{Use: "build", Hidden: true} // Hidden command to test
	configCmd := &cobra.Command{Use: "config"}
	deployCmd := &cobra.Command{Use: "deploy"}
	downCmd := &cobra.Command{Use: "down"}
	envCmd := &cobra.Command{Use: "env"}
	extensionCmd := &cobra.Command{Use: "extension"}
	infraCmd := &cobra.Command{Use: "infra"}
	initCmd := &cobra.Command{Use: "init"}
	monitorCmd := &cobra.Command{Use: "monitor"}
	packageCmd := &cobra.Command{Use: "package"}
	pipelineCmd := &cobra.Command{Use: "pipeline"}
	provisionCmd := &cobra.Command{Use: "provision"}
	upCmd := &cobra.Command{Use: "up"}
	versionCmd := &cobra.Command{Use: "version"}

	// Add subcommands
	authCmd.AddCommand(&cobra.Command{Use: "login"})
	authCmd.AddCommand(&cobra.Command{Use: "logout"})

	infraCmd.AddCommand(&cobra.Command{Use: "create"})
	infraCmd.AddCommand(&cobra.Command{Use: "delete"})
	infraCmd.AddCommand(&cobra.Command{Use: "generate"})

	pipelineCmd.AddCommand(&cobra.Command{Use: "config"})

	// Add all commands to root
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(envCmd)
	rootCmd.AddCommand(extensionCmd)
	rootCmd.AddCommand(infraCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(monitorCmd)
	rootCmd.AddCommand(packageCmd)
	rootCmd.AddCommand(pipelineCmd)
	rootCmd.AddCommand(provisionCmd)
	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(versionCmd)

	return rootCmd
}

func TestCommandMatcher_ResolveCommand(t *testing.T) {
	matcher := NewCommandMatcher()

	// Initialize with test Cobra command tree
	testCmd := createTestCobraCommand()
	matcher.InitializeFromCobraCommand(testCmd)

	testCases := []struct {
		name        string
		input       []string
		expected    []string
		expectError bool
		errorMsg    string
	}{
		{
			name:     "exact match",
			input:    []string{"pipeline"},
			expected: []string{"pipeline"},
		},
		{
			name:     "single character unique prefix - build",
			input:    []string{"b"},
			expected: []string{"build"},
		},
		{
			name:     "multi character prefix - pipeline",
			input:    []string{"pi"},
			expected: []string{"pipeline"},
		},
		{
			name:     "subcommand expansion - auth login",
			input:    []string{"au", "logi"},
			expected: []string{"auth", "login"},
		},
		{
			name:     "infra subcommand with correct prefixes",
			input:    []string{"inf", "c"},
			expected: []string{"infra", "create"},
		},
		{
			name:     "infra delete",
			input:    []string{"inf", "d"},
			expected: []string{"infra", "delete"},
		},
		{
			name:        "ambiguous command - p",
			input:       []string{"p"},
			expectError: true,
			errorMsg:    "ambiguous command",
		},
		{
			name:        "ambiguous command - i",
			input:       []string{"i"},
			expectError: true,
			errorMsg:    "ambiguous command",
		},
		{
			name:     "no match returns original",
			input:    []string{"nonexistent"},
			expected: []string{"nonexistent"},
		},
		{
			name:     "empty args",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "partial command with full subcommand",
			input:    []string{"au", "login"},
			expected: []string{"auth", "login"},
		},
		{
			name:     "up command",
			input:    []string{"u"},
			expected: []string{"up"},
		},
		{
			name:     "down command with correct prefix",
			input:    []string{"do"},
			expected: []string{"down"},
		},
		{
			name:     "version command with correct prefix",
			input:    []string{"ve"},
			expected: []string{"version"},
		},
		{
			name:     "monitor command",
			input:    []string{"m"},
			expected: []string{"monitor"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := matcher.ResolveCommand(tc.input)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
					return
				}
				if !strings.Contains(err.Error(), tc.errorMsg) {
					t.Errorf("Expected error message to contain '%s', got: %s", tc.errorMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(result) != len(tc.expected) {
				t.Errorf("Expected %v, got %v", tc.expected, result)
				return
			}

			for i, expected := range tc.expected {
				if result[i] != expected {
					t.Errorf("Expected %v, got %v", tc.expected, result)
					break
				}
			}
		})
	}
}

func TestCommandMatcher_GetSuggestions(t *testing.T) {
	matcher := NewCommandMatcher()

	// Initialize with test Cobra command tree
	testCmd := createTestCobraCommand()
	matcher.InitializeFromCobraCommand(testCmd)

	testCases := []struct {
		name     string
		prefix   string
		expected []string
	}{
		{
			name:     "p prefix suggestions",
			prefix:   "p",
			expected: []string{"package", "pipeline", "provision"},
		},
		{
			name:     "a prefix suggestions",
			prefix:   "a",
			expected: []string{"add", "auth"},
		},
		{
			name:     "no matches",
			prefix:   "xyz",
			expected: []string{},
		},
		{
			name:     "exact match",
			prefix:   "build",
			expected: []string{"build"},
		},
		{
			name:     "ext prefix suggestions",
			prefix:   "ext",
			expected: []string{"extension"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := matcher.GetSuggestions(tc.prefix)

			if len(result) != len(tc.expected) {
				t.Errorf("Expected %v, got %v", tc.expected, result)
				return
			}

			for i, expected := range tc.expected {
				if result[i] != expected {
					t.Errorf("Expected %v, got %v", tc.expected, result)
					break
				}
			}
		})
	}
}

func TestMinPrefixCalculation(t *testing.T) {
	matcher := NewCommandMatcher()
	
	// Initialize with test Cobra command tree
	testCmd := createTestCobraCommand()
	matcher.InitializeFromCobraCommand(testCmd)

	testCases := []struct {
		command   string
		minPrefix int
	}{
		{"pipeline", 2},  // "pi" to distinguish from "package" and "provision"
		{"package", 2},   // "pa" to distinguish from "pipeline" and "provision"
		{"provision", 2}, // "pr" to distinguish from "pipeline" and "package"
		{"build", 1},     // "b" is unique
		{"auth", 2},      // "au" to distinguish from "add"
		{"add", 2},       // "ad" to distinguish from "auth"
		{"up", 1},        // "u" is unique
		{"down", 2},      // "do" to distinguish from "deploy"
		{"deploy", 2},    // "de" to distinguish from "down"
		{"version", 1},   // "v" is unique in our test setup
		{"monitor", 1},   // "m" is unique
	}

	for _, tc := range testCases {
		t.Run(tc.command, func(t *testing.T) {
			if node, exists := matcher.commands[tc.command]; exists {
				if node.MinPrefix != tc.minPrefix {
					t.Errorf("Expected min prefix %d for %s, got %d",
						tc.minPrefix, tc.command, node.MinPrefix)
				}
			} else {
				t.Errorf("Command %s not found in matcher", tc.command)
			}
		})
	}
}

func TestShortcutConfig(t *testing.T) {
	// Test that shortcut config can be created
	config := &ShortcutConfig{
		Enabled:            false,
		MinPrefixOverrides: make(map[string]int),
	}

	if config.Enabled {
		t.Error("Expected shortcuts to be disabled by default in test config")
	}

	// Test Apply function
	matcher := NewCommandMatcher()
	config.Apply(matcher) // Should not panic when shortcuts are disabled

	config.Enabled = true
	config.Apply(matcher) // Should not panic when shortcuts are enabled
}

func TestShouldSkipShortcuts(t *testing.T) {
	testCases := []struct {
		name     string
		args     []string
		expected bool
	}{
		{
			name:     "empty args",
			args:     []string{},
			expected: true,
		},
		{
			name:     "help command",
			args:     []string{"help"},
			expected: true,
		},
		{
			name:     "help flag",
			args:     []string{"--help"},
			expected: true,
		},
		{
			name:     "short help flag",
			args:     []string{"-h"},
			expected: true,
		},
		{
			name:     "version command",
			args:     []string{"version"},
			expected: true,
		},
		{
			name:     "version flag",
			args:     []string{"--version"},
			expected: true,
		},
		{
			name:     "regular command",
			args:     []string{"build"},
			expected: false,
		},
		{
			name:     "shortcut command",
			args:     []string{"b"},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ShouldSkipShortcuts(tc.args)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v for args %v", tc.expected, result, tc.args)
			}
		})
	}
}

func TestValidateShortcut(t *testing.T) {
	// Set up the global command matcher for testing
	oldCommandMatcher := commandMatcher
	defer func() { commandMatcher = oldCommandMatcher }()
	
	commandMatcher = NewCommandMatcher()
	testCmd := createTestCobraCommand()
	commandMatcher.InitializeFromCobraCommand(testCmd)

	testCases := []struct {
		name        string
		shortcut    string
		expected    string
		expectError bool
	}{
		{
			name:     "valid shortcut",
			shortcut: "b",
			expected: "build",
		},
		{
			name:     "valid shortcut - pipeline",
			shortcut: "pi",
			expected: "pipeline",
		},
		{
			name:        "ambiguous shortcut",
			shortcut:    "p",
			expectError: true,
		},
		{
			name:     "non-existent shortcut",
			shortcut: "xyz",
			expected: "xyz", // Returns original if no match
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ValidateShortcut(tc.shortcut)

			if tc.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if result != tc.expected {
				t.Errorf("Expected %s, got %s", tc.expected, result)
			}
		})
	}
}
