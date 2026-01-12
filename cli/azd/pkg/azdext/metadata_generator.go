// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// GenerateExtensionMetadata generates ExtensionCommandMetadata from a Cobra root command
// This function is typically called by extensions to generate their metadata
func GenerateExtensionMetadata(schemaVersion, id, version string, root *cobra.Command) *extensions.ExtensionCommandMetadata {
	return &extensions.ExtensionCommandMetadata{
		SchemaVersion: schemaVersion,
		ID:            id,
		Version:       version,
		Commands:      generateCommands(root),
	}
}

// generateCommands recursively generates Command metadata from a Cobra command tree
func generateCommands(cmd *cobra.Command) []extensions.Command {
	var commands []extensions.Command

	for _, subCmd := range cmd.Commands() {
		command := generateCommand(subCmd)
		commands = append(commands, command)
	}

	return commands
}

// generateCommand generates Command metadata from a single Cobra command
func generateCommand(cmd *cobra.Command) extensions.Command {
	// Build command path
	path := buildCommandPath(cmd)

	command := extensions.Command{
		Name:     path,
		Short:    cmd.Short,
		Long:     cmd.Long,
		Usage:    cmd.UseLine(),
		Examples: generateExamples(cmd),
		Args:     generateArgs(cmd),
		Flags:    generateFlags(cmd),
		Hidden:   cmd.Hidden,
		Aliases:  cmd.Aliases,
	}

	if cmd.Deprecated != "" {
		command.Deprecated = cmd.Deprecated
	}

	// Recursively generate subcommands
	if cmd.HasSubCommands() {
		command.Subcommands = generateCommands(cmd)
	}

	return command
}

// buildCommandPath builds the full command path for a Cobra command
func buildCommandPath(cmd *cobra.Command) []string {
	var path []string
	current := cmd

	// Walk up the command tree to build the path
	for current != nil && current.Use != "" {
		// Extract command name from Use (format: "name [flags]" or "name")
		use := current.Use
		name := use
		for i, r := range use {
			if r == ' ' || r == '\t' {
				name = use[:i]
				break
			}
		}
		path = append([]string{name}, path...)
		current = current.Parent()
	}

	// Remove root command name (typically the binary name)
	if len(path) > 0 {
		path = path[1:]
	}

	return path
}

// generateExamples generates CommandExample metadata from Cobra command examples
func generateExamples(cmd *cobra.Command) []extensions.CommandExample {
	if cmd.Example == "" {
		return nil
	}

	// Cobra stores examples as a single string with multiple examples
	// For now, we'll return it as a single example
	// Extension developers can customize this if needed
	return []extensions.CommandExample{
		{
			Description: "Usage example",
			Command:     cmd.Example,
		},
	}
}

// generateArgs generates Argument metadata from Cobra command arguments
// Note: Cobra doesn't have built-in argument metadata, so we provide a best-effort approach
func generateArgs(cmd *cobra.Command) []extensions.Argument {
	// Cobra doesn't expose detailed argument metadata
	// Extension developers should manually define argument metadata if needed
	return nil
}

// generateFlags generates Flag metadata from Cobra command flags
func generateFlags(cmd *cobra.Command) []extensions.Flag {
	var flags []extensions.Flag

	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		// Skip hidden flags
		if flag.Hidden {
			return
		}

		flagMeta := extensions.Flag{
			Name:        flag.Name,
			Shorthand:   flag.Shorthand,
			Description: flag.Usage,
			Type:        getFlagType(flag),
			Hidden:      flag.Hidden,
		}

		if flag.DefValue != "" {
			flagMeta.Default = flag.DefValue
		}

		if flag.Deprecated != "" {
			flagMeta.Deprecated = flag.Deprecated
		}

		flags = append(flags, flagMeta)
	})

	return flags
}

// getFlagType maps Cobra/pflag types to metadata type strings
func getFlagType(flag *pflag.Flag) string {
	switch flag.Value.Type() {
	case "bool":
		return "bool"
	case "int", "int32", "int64":
		return "int"
	case "string":
		return "string"
	case "stringSlice", "stringArray":
		return "stringArray"
	case "intSlice":
		return "intArray"
	default:
		return "string"
	}
}
