// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"sort"
	"strings"
)

// PrintShortcutHelp prints helpful information about command shortcuts
func PrintShortcutHelp() {
	fmt.Println("Command Shortcuts:")
	fmt.Println("azd supports shortened commands for faster typing.")
	fmt.Println("Examples:")
	fmt.Println("  azd b        -> azd build")
	fmt.Println("  azd pi       -> azd pipeline")
	fmt.Println("  azd au logi  -> azd auth login")
	fmt.Println("  azd inf c    -> azd infra create")
	fmt.Println("")
	fmt.Println("Minimum prefixes to avoid ambiguity:")

	matcher := NewCommandMatcher()
	commands := make([]string, 0, len(matcher.commands))
	for cmd := range matcher.commands {
		commands = append(commands, cmd)
	}
	sort.Strings(commands)

	fmt.Printf("  %-15s %-15s %s\n", "Command", "Min Shortcut", "Conflicts")
	fmt.Printf("  %-15s %-15s %s\n", "-------", "------------", "---------")

	for _, cmd := range commands {
		if node := matcher.commands[cmd]; node != nil {
			if node.MinPrefix <= len(cmd) {
				shortcut := cmd[:node.MinPrefix]
				conflicts := getConflictingCommands(cmd, matcher)
				fmt.Printf("  %-15s %-15s %s\n", cmd, shortcut, conflicts)
			}
		}
	}

	fmt.Println("")
	fmt.Println("Use AZD_DISABLE_SHORTCUTS=true to disable shortcuts")
	fmt.Println("For more examples, see: https://github.com/Azure/azure-dev/blob/main/cli/azd/SHORTCUTS.md")
}

// getConflictingCommands returns a string describing what commands conflict with the given command
func getConflictingCommands(target string, matcher *CommandMatcher) string {
	if node, exists := matcher.commands[target]; exists && node.MinPrefix > 1 {
		conflicts := make([]string, 0)
		prefix := target[:node.MinPrefix-1]
		for cmd := range matcher.commands {
			if cmd != target && strings.HasPrefix(cmd, prefix) {
				conflicts = append(conflicts, cmd)
			}
		}
		if len(conflicts) > 0 {
			sort.Strings(conflicts)
			return strings.Join(conflicts, ", ")
		}
	}
	return "none"
}

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
