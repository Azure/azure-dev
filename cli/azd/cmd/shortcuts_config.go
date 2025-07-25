// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"strconv"
)

// ShortcutConfig holds configuration for command shortcuts
type ShortcutConfig struct {
	Enabled            bool
	MinPrefixOverrides map[string]int
}

// GetShortcutConfig reads shortcut configuration from environment
func GetShortcutConfig() *ShortcutConfig {
	config := &ShortcutConfig{
		Enabled:            true,
		MinPrefixOverrides: make(map[string]int),
	}

	// Check if shortcuts are disabled
	if disabled := os.Getenv("AZD_DISABLE_SHORTCUTS"); disabled != "" {
		if val, err := strconv.ParseBool(disabled); err == nil && val {
			config.Enabled = false
		}
	}

	// Additional configuration can be added here
	return config
}

// Apply applies configuration to a command matcher
func (sc *ShortcutConfig) Apply(matcher *CommandMatcher) {
	if !sc.Enabled {
		return
	}

	// Apply minimum prefix overrides
	for cmd, minPrefix := range sc.MinPrefixOverrides {
		if node, exists := matcher.commands[cmd]; exists {
			node.MinPrefix = minPrefix
		}
	}
}
