// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

const (
	// Configuration path for command shortcuts
	CommandShortcutsConfigPath = "command.shortcuts"
)

// ShortcutConfig holds configuration for command shortcuts
type ShortcutConfig struct {
	Enabled            bool
	MinPrefixOverrides map[string]int
}

// GetShortcutConfig reads shortcut configuration from azd user config
func GetShortcutConfig() *ShortcutConfig {
	shortcutConfig := &ShortcutConfig{
		Enabled:            false, // Disabled by default
		MinPrefixOverrides: make(map[string]int),
	}

	// Load azd user configuration
	manager := config.NewManager()
	fileConfigManager := config.NewFileConfigManager(manager)
	userConfigManager := config.NewUserConfigManager(fileConfigManager)
	azdConfig, err := userConfigManager.Load()
	if err != nil {
		// If config can't be loaded, shortcuts remain disabled
		return shortcutConfig
	}

	// Check if shortcuts are enabled in user config
	if value, ok := azdConfig.Get(CommandShortcutsConfigPath); ok {
		if enabledStr, ok := value.(string); ok {
			shortcutConfig.Enabled = (enabledStr == "on" || enabledStr == "true")
		}
	}

	// Additional configuration can be added here
	return shortcutConfig
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
