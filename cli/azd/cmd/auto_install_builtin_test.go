// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestIsBuiltInCommand(t *testing.T) {
	// Create a mock root command with some subcommands
	rootCmd := &cobra.Command{
		Use: "azd",
	}

	// Add some built-in commands
	upCmd := &cobra.Command{
		Use: "up",
	}
	rootCmd.AddCommand(upCmd)

	initCmd := &cobra.Command{
		Use:     "init",
		Aliases: []string{"initialize"},
	}
	rootCmd.AddCommand(initCmd)

	downCmd := &cobra.Command{
		Use: "down",
	}
	rootCmd.AddCommand(downCmd)

	tests := []struct {
		name        string
		commandName string
		expected    bool
	}{
		{
			name:        "built-in command up returns true",
			commandName: "up",
			expected:    true,
		},
		{
			name:        "built-in command init returns true",
			commandName: "init",
			expected:    true,
		},
		{
			name:        "built-in command down returns true",
			commandName: "down",
			expected:    true,
		},
		{
			name:        "command alias initialize returns true",
			commandName: "initialize",
			expected:    true,
		},
		{
			name:        "non-existent command returns false",
			commandName: "demo",
			expected:    false,
		},
		{
			name:        "empty command name returns false",
			commandName: "",
			expected:    false,
		},
		{
			name:        "unknown command returns false",
			commandName: "nonexistent",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isBuiltInCommand(rootCmd, tt.commandName)
			if result != tt.expected {
				t.Errorf("isBuiltInCommand(%q) = %v, expected %v", tt.commandName, result, tt.expected)
			}
		})
	}
}

func TestHasSubcommand(t *testing.T) {
	// Create a command with subcommands
	parentCmd := &cobra.Command{Use: "parent"}

	childCmd := &cobra.Command{
		Use:     "child",
		Aliases: []string{"c", "kid"},
	}
	parentCmd.AddCommand(childCmd)

	otherCmd := &cobra.Command{Use: "other"}
	parentCmd.AddCommand(otherCmd)

	tests := []struct {
		name     string
		cmdName  string
		expected bool
	}{
		{
			name:     "existing subcommand returns true",
			cmdName:  "child",
			expected: true,
		},
		{
			name:     "alias returns true",
			cmdName:  "c",
			expected: true,
		},
		{
			name:     "another alias returns true",
			cmdName:  "kid",
			expected: true,
		},
		{
			name:     "other subcommand returns true",
			cmdName:  "other",
			expected: true,
		},
		{
			name:     "non-existent subcommand returns false",
			cmdName:  "nonexistent",
			expected: false,
		},
		{
			name:     "empty name returns false",
			cmdName:  "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasSubcommand(parentCmd, tt.cmdName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetCommandPath(t *testing.T) {
	// Build a command hierarchy: root -> ai -> agent
	rootCmd := &cobra.Command{Use: "azd"}
	aiCmd := &cobra.Command{Use: "ai"}
	agentCmd := &cobra.Command{Use: "agent"}

	rootCmd.AddCommand(aiCmd)
	aiCmd.AddCommand(agentCmd)

	tests := []struct {
		name     string
		cmd      *cobra.Command
		expected []string
	}{
		{
			name:     "root command returns empty path",
			cmd:      rootCmd,
			expected: nil,
		},
		{
			name:     "first level command returns single element",
			cmd:      aiCmd,
			expected: []string{"ai"},
		},
		{
			name:     "nested command returns full path",
			cmd:      agentCmd,
			expected: []string{"ai", "agent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getCommandPath(tt.cmd)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildNamespaceArgs(t *testing.T) {
	// Build a command hierarchy: root -> ai
	rootCmd := &cobra.Command{Use: "azd"}
	aiCmd := &cobra.Command{Use: "ai"}
	rootCmd.AddCommand(aiCmd)

	tests := []struct {
		name          string
		cmd           *cobra.Command
		remainingArgs []string
		expected      []string
	}{
		{
			name:          "command path with remaining args",
			cmd:           aiCmd,
			remainingArgs: []string{"agent", "init"},
			expected:      []string{"ai", "agent", "init"},
		},
		{
			name:          "command path with flags filtered out",
			cmd:           aiCmd,
			remainingArgs: []string{"agent", "--help", "init", "-v"},
			expected:      []string{"ai", "agent", "init"},
		},
		{
			name:          "command path with no remaining args",
			cmd:           aiCmd,
			remainingArgs: []string{},
			expected:      []string{"ai"},
		},
		{
			name:          "command path with only flags",
			cmd:           aiCmd,
			remainingArgs: []string{"--help", "-v"},
			expected:      []string{"ai"},
		},
		{
			name:          "root command with remaining args",
			cmd:           rootCmd,
			remainingArgs: []string{"demo", "init"},
			expected:      []string{"demo", "init"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildNamespaceArgs(tt.cmd, tt.remainingArgs)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPartialNamespaceDetection(t *testing.T) {
	// This test verifies the logic for detecting when auto-install should trigger
	// for partial namespace matches vs when an extension command should handle args.

	// Scenario: ai.finetuning and ai.agent extensions both installed
	// Command tree: azd -> ai -> finetuning (extension leaf)
	//                        -> agent (extension leaf)
	rootCmd := &cobra.Command{Use: "azd"}
	aiCmd := &cobra.Command{Use: "ai", Short: "Commands for the ai extension namespace."}
	rootCmd.AddCommand(aiCmd)

	// Extension leaf commands have annotations
	finetuningCmd := &cobra.Command{
		Use:   "finetuning",
		Short: "Finetuning extension",
		Annotations: map[string]string{
			"extension.id":        "azure.ai.finetune",
			"extension.namespace": "ai.finetuning",
		},
	}
	aiCmd.AddCommand(finetuningCmd)

	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Agent extension",
		Annotations: map[string]string{
			"extension.id":        "azure.ai.agents",
			"extension.namespace": "ai.agent",
		},
	}
	aiCmd.AddCommand(agentCmd)

	tests := []struct {
		name                   string
		args                   []string
		expectExtensionCommand bool // true if Find should return an extension command
		expectRemainingArgs    []string
	}{
		{
			name:                   "azd ai agent version - finds extension command",
			args:                   []string{"ai", "agent", "version"},
			expectExtensionCommand: true,
			expectRemainingArgs:    []string{"version"},
		},
		{
			name:                   "azd ai finetuning start - finds extension command",
			args:                   []string{"ai", "finetuning", "start"},
			expectExtensionCommand: true,
			expectRemainingArgs:    []string{"start"},
		},
		{
			name:                   "azd ai - finds namespace command (no extension.id)",
			args:                   []string{"ai"},
			expectExtensionCommand: false,
			expectRemainingArgs:    []string{},
		},
		{
			name:                   "azd ai unknown - finds namespace command with unknown subcommand",
			args:                   []string{"ai", "unknown"},
			expectExtensionCommand: false,
			expectRemainingArgs:    []string{"unknown"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			foundCmd, remaining, err := rootCmd.Find(tt.args)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectRemainingArgs, remaining)

			_, isExtensionCmd := foundCmd.Annotations["extension.id"]
			assert.Equal(t, tt.expectExtensionCommand, isExtensionCmd,
				"expected extension command: %v, got command %q with annotations %v",
				tt.expectExtensionCommand, foundCmd.Name(), foundCmd.Annotations)
		})
	}
}
