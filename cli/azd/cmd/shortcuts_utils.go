// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
)

// ValidateShortcut checks if a shortcut is valid and unambiguous
func ValidateShortcut(shortcut string) (string, error) {
	// Use the global command matcher if available, otherwise create a new one
	var matcher *CommandMatcher
	if commandMatcher != nil {
		matcher = commandMatcher
	} else {
		// For testing or when global matcher is not initialized
		matcher = NewCommandMatcher()
		// Note: In a real scenario, this would need to be initialized with the actual command tree
		// For testing, this will only work with mocked scenarios
	}

	expanded, err := matcher.ResolveCommand([]string{shortcut})
	if err != nil {
		return "", err
	}
	if len(expanded) == 0 {
		return shortcut, nil // Return original if no expansion
	}
	return expanded[0], nil
}

// GetAllShortcuts returns a map of all valid shortcuts
func GetAllShortcuts() map[string]string {
	matcher := NewCommandMatcher()
	shortcuts := make(map[string]string)

	for cmd, node := range matcher.commands {
		// Add the minimum prefix shortcut
		if node.MinPrefix <= len(cmd) {
			shortcuts[cmd[:node.MinPrefix]] = cmd
		}

		// Add all valid prefixes
		for i := node.MinPrefix; i < len(cmd); i++ {
			prefix := cmd[:i+1]
			if expanded, err := matcher.expandCommand(prefix, matcher.commands); err == nil {
				shortcuts[prefix] = expanded
			}
		}
	}

	return shortcuts
}

// IsShortcutDisabled checks if shortcuts are disabled via environment variable
func IsShortcutDisabled() bool {
	config := GetShortcutConfig()
	return !config.Enabled
}

// ShouldSkipShortcuts determines if shortcut processing should be skipped for given args
func ShouldSkipShortcuts(args []string) bool {
	if len(args) == 0 {
		return true
	}

	// Skip shortcut processing for help and version commands
	firstArg := strings.ToLower(args[0])
	skipCommands := []string{"help", "--help", "-h", "version", "--version", "-v"}

	for _, skipCmd := range skipCommands {
		if firstArg == skipCmd {
			return true
		}
	}

	return false
}
