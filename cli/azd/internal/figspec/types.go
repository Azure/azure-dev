// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package figspec generates Fig autocomplete specifications from Cobra commands.
package figspec

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Spec represents a Fig autocomplete specification
type Spec struct {
	Name        string
	Description string
	Subcommands []Subcommand
	Options     []Option
	Args        []Arg
}

// Subcommand represents a subcommand in the Fig spec
type Subcommand struct {
	Name        []string
	Description string
	Subcommands []Subcommand
	Options     []Option
	Args        []Arg
	Hidden      bool
}

// Option represents a flag/option in the Fig spec
type Option struct {
	Name         []string
	Description  string
	Args         []Arg
	IsPersistent bool
	IsRepeatable bool
	IsRequired   bool
	IsDangerous  bool
	Hidden       bool
}

// Arg represents an argument in the Fig spec
type Arg struct {
	Name        string
	Description string
	IsOptional  bool
	Suggestions []string
	Generator   string
	Template    string
}

// CommandContext contains information about a command for custom processing
type CommandContext struct {
	Command     *cobra.Command
	CommandPath string
	Parent      *CommandContext
}

// FlagContext contains information about a flag for custom processing
type FlagContext struct {
	Flag        *pflag.Flag
	CommandPath string
}

// CustomSuggestionProvider provides custom suggestions for specific flags
type CustomSuggestionProvider interface {
	// GetSuggestions returns custom suggestions for a flag if applicable
	// Returns nil if no custom suggestions are needed
	GetSuggestions(ctx *FlagContext) []string
}

// CustomGeneratorProvider provides custom generator names for specific arguments
type CustomGeneratorProvider interface {
	// GetCommandArgGenerator returns a generator name for a specific command argument if applicable
	// argName is the name of the argument extracted from the command's Use field
	// Returns empty string if no custom generator is needed
	GetCommandArgGenerator(ctx *CommandContext, argName string) string

	// GetFlagGenerator returns a generator name for a flag's argument if applicable
	// Returns empty string if no custom generator is needed
	GetFlagGenerator(ctx *FlagContext) string
}

// CustomArgsProvider provides custom argument specifications for specific commands
type CustomArgsProvider interface {
	// GetCommandArgs returns custom argument specifications for a command if applicable
	// Returns nil if no custom args are needed (default parsing will be used)
	GetCommandArgs(ctx *CommandContext) []Arg
}

// CustomFlagArgsProvider provides custom argument specifications for specific flags
type CustomFlagArgsProvider interface {
	// GetFlagArgs returns custom argument specification for a flag if applicable
	// Returns nil if no custom args are needed (default parsing will be used)
	GetFlagArgs(ctx *FlagContext) *Arg
}
