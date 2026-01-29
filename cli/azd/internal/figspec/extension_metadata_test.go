// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
)

func TestConvertExtensionCommand(t *testing.T) {
	// Simulate global flags as they would be collected from root.PersistentFlags()
	globalFlagNames := map[string]bool{
		"help":           true,
		"docs":           true,
		"cwd":            true,
		"debug":          true,
		"no-prompt":      true,
		"trace-log-file": true,
		"trace-log-url":  true,
	}

	tests := []struct {
		name          string
		cmd           extensions.Command
		includeHidden bool
		wantNil       bool
		wantName      []string
		wantDesc      string
		wantSubcmds   int
		wantOptions   int
		wantArgs      int
	}{
		{
			name: "basic command",
			cmd: extensions.Command{
				Name:  []string{"context"},
				Short: "Display context info",
			},
			includeHidden: false,
			wantNil:       false,
			wantName:      []string{"context"},
			wantDesc:      "Display context info",
			wantSubcmds:   0,
			wantOptions:   0,
			wantArgs:      0,
		},
		{
			name: "command with aliases",
			cmd: extensions.Command{
				Name:    []string{"list"},
				Short:   "List items",
				Aliases: []string{"ls", "l"},
			},
			includeHidden: false,
			wantNil:       false,
			wantName:      []string{"list", "ls", "l"},
			wantDesc:      "List items",
			wantSubcmds:   0,
			wantOptions:   0,
			wantArgs:      0,
		},
		{
			name: "hidden command not included",
			cmd: extensions.Command{
				Name:   []string{"hidden"},
				Short:  "Hidden command",
				Hidden: true,
			},
			includeHidden: false,
			wantNil:       true,
		},
		{
			name: "hidden command included when flag set",
			cmd: extensions.Command{
				Name:   []string{"hidden"},
				Short:  "Hidden command",
				Hidden: true,
			},
			includeHidden: true,
			wantNil:       false,
			wantName:      []string{"hidden"},
			wantDesc:      "Hidden command",
			wantSubcmds:   0,
			wantOptions:   0,
			wantArgs:      0,
		},
		{
			name: "command with flags",
			cmd: extensions.Command{
				Name:  []string{"run"},
				Short: "Run something",
				Flags: []extensions.Flag{
					{Name: "verbose", Shorthand: "v", Description: "Enable verbose output", Type: "bool"},
					{Name: "output", Shorthand: "o", Description: "Output format", Type: "string"},
				},
			},
			includeHidden: false,
			wantNil:       false,
			wantName:      []string{"run"},
			wantDesc:      "Run something",
			wantSubcmds:   0,
			wantOptions:   2,
			wantArgs:      0,
		},
		{
			name: "global flags are filtered out",
			cmd: extensions.Command{
				Name:  []string{"test"},
				Short: "Test command",
				Flags: []extensions.Flag{
					{Name: "custom", Description: "Custom flag", Type: "bool"},
					{Name: "help", Shorthand: "h", Description: "Help", Type: "bool"},
					{Name: "no-prompt", Description: "No prompt", Type: "bool"},
					{Name: "debug", Description: "Debug", Type: "bool"},
					{Name: "cwd", Shorthand: "C", Description: "Working dir", Type: "string"},
				},
			},
			includeHidden: false,
			wantNil:       false,
			wantName:      []string{"test"},
			wantDesc:      "Test command",
			wantSubcmds:   0,
			wantOptions:   1, // Only "custom" flag should be included
			wantArgs:      0,
		},
		{
			name: "command with args",
			cmd: extensions.Command{
				Name:  []string{"get"},
				Short: "Get an item",
				Args: []extensions.Argument{
					{Name: "name", Description: "Item name", Required: true},
					{Name: "version", Description: "Optional version", Required: false},
				},
			},
			includeHidden: false,
			wantNil:       false,
			wantName:      []string{"get"},
			wantDesc:      "Get an item",
			wantSubcmds:   0,
			wantOptions:   0,
			wantArgs:      2,
		},
		{
			name: "command with subcommands",
			cmd: extensions.Command{
				Name:  []string{"service"},
				Short: "Service commands",
				Subcommands: []extensions.Command{
					{Name: []string{"service", "start"}, Short: "Start service"},
					{Name: []string{"service", "stop"}, Short: "Stop service"},
				},
			},
			includeHidden: false,
			wantNil:       false,
			wantName:      []string{"service"},
			wantDesc:      "Service commands",
			wantSubcmds:   2,
			wantOptions:   0,
			wantArgs:      0,
		},
		{
			name:          "empty name returns nil",
			cmd:           extensions.Command{Name: []string{}, Short: "Empty"},
			includeHidden: false,
			wantNil:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertExtensionCommand(tt.cmd, tt.includeHidden, globalFlagNames)

			if tt.wantNil {
				require.Nil(t, result)
				return
			}

			require.NotNil(t, result)
			require.Equal(t, tt.wantName, result.Name)
			require.Equal(t, tt.wantDesc, result.Description)
			require.Len(t, result.Subcommands, tt.wantSubcmds)
			require.Len(t, result.Options, tt.wantOptions)
			require.Len(t, result.Args, tt.wantArgs)
		})
	}
}

func TestConvertExtensionFlag(t *testing.T) {
	tests := []struct {
		name            string
		flag            extensions.Flag
		wantName        []string
		wantDesc        string
		wantRequired    bool
		wantRepeatable  bool
		wantDangerous   bool
		wantArgsCount   int
		wantSuggestions []string
	}{
		{
			name: "simple bool flag",
			flag: extensions.Flag{
				Name:        "verbose",
				Shorthand:   "v",
				Description: "Verbose output",
				Type:        "bool",
			},
			wantName:        []string{"--verbose", "-v"},
			wantDesc:        "Verbose output",
			wantRequired:    false,
			wantRepeatable:  false,
			wantDangerous:   false,
			wantArgsCount:   0,
			wantSuggestions: nil,
		},
		{
			name: "string flag with valid values",
			flag: extensions.Flag{
				Name:        "format",
				Description: "Output format",
				Type:        "string",
				ValidValues: []string{"json", "table", "yaml"},
			},
			wantName:        []string{"--format"},
			wantDesc:        "Output format",
			wantRequired:    false,
			wantRepeatable:  false,
			wantDangerous:   false,
			wantArgsCount:   1,
			wantSuggestions: []string{"json", "table", "yaml"},
		},
		{
			name: "required flag",
			flag: extensions.Flag{
				Name:        "name",
				Description: "Item name",
				Type:        "string",
				Required:    true,
			},
			wantName:        []string{"--name"},
			wantDesc:        "Item name",
			wantRequired:    true,
			wantRepeatable:  false,
			wantDangerous:   false,
			wantArgsCount:   1,
			wantSuggestions: nil,
		},
		{
			name: "array flag is repeatable",
			flag: extensions.Flag{
				Name:        "tag",
				Description: "Tags to apply",
				Type:        "stringArray",
			},
			wantName:        []string{"--tag"},
			wantDesc:        "Tags to apply",
			wantRequired:    false,
			wantRepeatable:  true,
			wantDangerous:   false,
			wantArgsCount:   1,
			wantSuggestions: nil,
		},
		{
			name: "force flag is dangerous",
			flag: extensions.Flag{
				Name:        "force",
				Description: "Force operation",
				Type:        "bool",
			},
			wantName:        []string{"--force"},
			wantDesc:        "Force operation",
			wantRequired:    false,
			wantRepeatable:  false,
			wantDangerous:   true,
			wantArgsCount:   0,
			wantSuggestions: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertExtensionFlag(tt.flag)

			require.Equal(t, tt.wantName, result.Name)
			require.Equal(t, tt.wantDesc, result.Description)
			require.Equal(t, tt.wantRequired, result.IsRequired)
			require.Equal(t, tt.wantRepeatable, result.IsRepeatable)
			require.Equal(t, tt.wantDangerous, result.IsDangerous)
			require.Len(t, result.Args, tt.wantArgsCount)

			if tt.wantArgsCount > 0 && tt.wantSuggestions != nil {
				require.Equal(t, tt.wantSuggestions, result.Args[0].Suggestions)
			}
		})
	}
}

func TestConvertExtensionArg(t *testing.T) {
	tests := []struct {
		name            string
		arg             extensions.Argument
		wantName        string
		wantDesc        string
		wantOptional    bool
		wantSuggestions []string
	}{
		{
			name: "required arg",
			arg: extensions.Argument{
				Name:        "filename",
				Description: "File to process",
				Required:    true,
			},
			wantName:        "filename",
			wantDesc:        "File to process",
			wantOptional:    false,
			wantSuggestions: nil,
		},
		{
			name: "optional arg",
			arg: extensions.Argument{
				Name:        "version",
				Description: "Optional version",
				Required:    false,
			},
			wantName:        "version",
			wantDesc:        "Optional version",
			wantOptional:    true,
			wantSuggestions: nil,
		},
		{
			name: "arg with valid values",
			arg: extensions.Argument{
				Name:        "environment",
				Description: "Target environment",
				Required:    true,
				ValidValues: []string{"dev", "staging", "prod"},
			},
			wantName:        "environment",
			wantDesc:        "Target environment",
			wantOptional:    false,
			wantSuggestions: []string{"dev", "staging", "prod"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertExtensionArg(tt.arg)

			require.Equal(t, tt.wantName, result.Name)
			require.Equal(t, tt.wantDesc, result.Description)
			require.Equal(t, tt.wantOptional, result.IsOptional)
			require.Equal(t, tt.wantSuggestions, result.Suggestions)
		})
	}
}

func TestConvertExtensionCommandForHelp(t *testing.T) {
	tests := []struct {
		name          string
		cmd           extensions.Command
		includeHidden bool
		wantNil       bool
		wantName      []string
		wantDesc      string
		wantSubcmds   int
	}{
		{
			name: "basic command",
			cmd: extensions.Command{
				Name:  []string{"context"},
				Short: "Display context info",
			},
			includeHidden: false,
			wantNil:       false,
			wantName:      []string{"context"},
			wantDesc:      "Display context info",
			wantSubcmds:   0,
		},
		{
			name: "command with aliases",
			cmd: extensions.Command{
				Name:    []string{"list"},
				Short:   "List items",
				Aliases: []string{"ls", "l"},
			},
			includeHidden: false,
			wantNil:       false,
			wantName:      []string{"list", "ls", "l"},
			wantDesc:      "List items",
			wantSubcmds:   0,
		},
		{
			name: "hidden command not included",
			cmd: extensions.Command{
				Name:   []string{"hidden"},
				Short:  "Hidden command",
				Hidden: true,
			},
			includeHidden: false,
			wantNil:       true,
		},
		{
			name: "hidden command included when flag set",
			cmd: extensions.Command{
				Name:   []string{"hidden"},
				Short:  "Hidden command",
				Hidden: true,
			},
			includeHidden: true,
			wantNil:       false,
			wantName:      []string{"hidden"},
			wantDesc:      "Hidden command",
			wantSubcmds:   0,
		},
		{
			name: "command with subcommands (flags/args excluded in help)",
			cmd: extensions.Command{
				Name:  []string{"service"},
				Short: "Service commands",
				Flags: []extensions.Flag{
					{Name: "verbose", Type: "bool"},
				},
				Args: []extensions.Argument{
					{Name: "name", Required: true},
				},
				Subcommands: []extensions.Command{
					{Name: []string{"service", "start"}, Short: "Start service"},
					{Name: []string{"service", "stop"}, Short: "Stop service"},
				},
			},
			includeHidden: false,
			wantNil:       false,
			wantName:      []string{"service"},
			wantDesc:      "Service commands",
			wantSubcmds:   2,
		},
		{
			name:          "empty name returns nil",
			cmd:           extensions.Command{Name: []string{}, Short: "Empty"},
			includeHidden: false,
			wantNil:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertExtensionCommandForHelp(tt.cmd, tt.includeHidden)

			if tt.wantNil {
				require.Nil(t, result)
				return
			}

			require.NotNil(t, result)
			require.Equal(t, tt.wantName, result.Name)
			require.Equal(t, tt.wantDesc, result.Description)
			require.Len(t, result.Subcommands, tt.wantSubcmds)
			// Help subcommands should not include options or args
			require.Empty(t, result.Options)
			require.Empty(t, result.Args)
		})
	}
}
