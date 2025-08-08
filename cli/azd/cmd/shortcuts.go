// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// CommandMatcher handles prefix matching for command shortcuts
type CommandMatcher struct {
	commands map[string]*CommandNode
	registry map[string][]string // Maps prefixes to possible commands
}

// CommandNode represents a command in the hierarchy
type CommandNode struct {
	FullName    string
	Children    map[string]*CommandNode
	IsLeaf      bool
	MinPrefix   int
	SubCommands []string
}

// NewCommandMatcher creates a new command matcher with the current CLI commands
func NewCommandMatcher() *CommandMatcher {
	cm := &CommandMatcher{
		commands: make(map[string]*CommandNode),
		registry: make(map[string][]string),
	}

	// Commands will be initialized later after the root command is built
	return cm
}

// InitializeFromCobraCommand builds the command registry from a Cobra command tree
func (cm *CommandMatcher) InitializeFromCobraCommand(rootCmd *cobra.Command) {
	cm.commands = make(map[string]*CommandNode)
	cm.registry = make(map[string][]string)

	// Build command nodes from the actual Cobra command structure
	for _, cmd := range rootCmd.Commands() {
		// Include all commands, even hidden ones, for shortcuts
		// Hidden commands are still functional, just not shown in help

		cmdName := cmd.Name()
		subCommands := make([]string, 0)
		children := make(map[string]*CommandNode)

		// Get subcommands
		for _, subCmd := range cmd.Commands() {
			// Include hidden subcommands too
			subCmdName := subCmd.Name()
			subCommands = append(subCommands, subCmdName)
			children[subCmdName] = &CommandNode{
				FullName: subCmdName,
				Children: make(map[string]*CommandNode),
				IsLeaf:   true,
			}
		}

		node := &CommandNode{
			FullName:    cmdName,
			Children:    children,
			IsLeaf:      len(subCommands) == 0,
			SubCommands: subCommands,
		}

		cm.commands[cmdName] = node
	}

	// Calculate minimum prefixes to avoid ambiguity
	cm.calculateMinPrefixes()
}

// calculateMinPrefixes determines the minimum prefix length needed for each command
func (cm *CommandMatcher) calculateMinPrefixes() {
	commandNames := make([]string, 0, len(cm.commands))
	for name := range cm.commands {
		commandNames = append(commandNames, name)
	}

	for _, cmd := range commandNames {
		cm.commands[cmd].MinPrefix = cm.findMinPrefix(cmd, commandNames)
	}
}

// findMinPrefix finds the minimum prefix length to uniquely identify a command
func (cm *CommandMatcher) findMinPrefix(target string, allCommands []string) int {
	for i := 1; i <= len(target); i++ {
		prefix := target[:i]
		matches := 0
		for _, cmd := range allCommands {
			if strings.HasPrefix(cmd, prefix) {
				matches++
			}
		}
		if matches == 1 {
			return i
		}
	}
	return len(target)
}

// ResolveCommand expands shortened commands to their full forms
func (cm *CommandMatcher) ResolveCommand(args []string) ([]string, error) {
	if len(args) == 0 {
		return args, nil
	}

	result := make([]string, len(args))
	copy(result, args)

	// Resolve first argument (main command)
	expanded, err := cm.expandCommand(args[0], cm.commands)
	if err != nil {
		return nil, err
	}
	result[0] = expanded

	// Resolve second argument (subcommand) if it exists
	if len(args) > 1 {
		mainCmd := result[0]
		if node, exists := cm.commands[mainCmd]; exists && len(node.Children) > 0 {
			expandedSub, err := cm.expandCommand(args[1], node.Children)
			if err != nil {
				return nil, err
			}
			result[1] = expandedSub
		}
	}

	return result, nil
}

// expandCommand expands a single command argument
func (cm *CommandMatcher) expandCommand(input string, commands map[string]*CommandNode) (string, error) {
	matches := make([]string, 0)

	// Find all commands that match the prefix
	for cmdName := range commands {
		if strings.HasPrefix(cmdName, input) {
			matches = append(matches, cmdName)
		}
	}

	switch len(matches) {
	case 0:
		// No matches, return original input
		return input, nil
	case 1:
		// Exact match found
		return matches[0], nil
	default:
		// Multiple matches - ambiguous
		return "", cm.createAmbiguityError(input, matches)
	}
}

// createAmbiguityError creates a helpful error message for ambiguous commands
func (cm *CommandMatcher) createAmbiguityError(input string, matches []string) error {
	sort.Strings(matches)

	var suggestions []string
	for _, match := range matches {
		// For subcommands, we need to find the minimum prefix within the parent command context
		minPrefixLen := cm.getMinPrefixForCommand(match, matches)
		if minPrefixLen <= len(match) {
			suggestion := fmt.Sprintf("  - %s (use \"azd %s\" or longer)", match, match[:minPrefixLen])
			suggestions = append(suggestions, suggestion)
		}
	}

	return fmt.Errorf("ambiguous command \"azd %s\"\nCould match:\n%s",
		input, strings.Join(suggestions, "\n"))
}

// getMinPrefixForCommand calculates minimum prefix needed for a command among given options
func (cm *CommandMatcher) getMinPrefixForCommand(target string, allCommands []string) int {
	for i := 1; i <= len(target); i++ {
		prefix := target[:i]
		matches := 0
		for _, cmd := range allCommands {
			if strings.HasPrefix(cmd, prefix) {
				matches++
			}
		}
		if matches == 1 {
			return i
		}
	}
	return len(target)
}

// GetSuggestions returns command suggestions for help/autocomplete
func (cm *CommandMatcher) GetSuggestions(prefix string) []string {
	matches := make([]string, 0)
	for cmdName := range cm.commands {
		if strings.HasPrefix(cmdName, prefix) {
			matches = append(matches, cmdName)
		}
	}
	sort.Strings(matches)
	return matches
}
