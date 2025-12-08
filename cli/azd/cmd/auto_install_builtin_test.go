// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
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
