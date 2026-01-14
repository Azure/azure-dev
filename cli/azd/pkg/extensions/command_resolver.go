// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"io"
	"slices"
	"strings"

	"github.com/spf13/pflag"
)

// ResolveCommandPath finds the best matching command path for the given args using extension metadata.
func ResolveCommandPath(metadata *ExtensionCommandMetadata, args []string) []string {
	if metadata == nil {
		return nil
	}

	match := matchCommand(metadata.Commands, args)
	if match == nil {
		return nil
	}

	return match.path
}

// ResolveCommandFlags returns the matching flag names for the command invoked by args.
func ResolveCommandFlags(metadata *ExtensionCommandMetadata, args []string) []string {
	if metadata == nil {
		return nil
	}

	match := matchCommand(metadata.Commands, args)
	if match == nil || len(match.command.Flags) == 0 {
		return nil
	}

	return parseFlags(args, match.command.Flags)
}

type commandEntry struct {
	path    []string
	command *Command
}

// matchCommand returns the longest matching command entry for the provided args.
// It searches the command tree recursively, matching both primary command names and aliases.
func matchCommand(commands []Command, args []string) *commandEntry {
	cmdArgs := extractCommandArgs(args)
	if len(cmdArgs) == 0 {
		return nil
	}

	var bestMatch *commandEntry

	var search func([]Command)
	search = func(cmds []Command) {
		for i := range cmds {
			cmd := &cmds[i]
			if len(cmd.Name) == 0 || len(cmd.Name) > len(cmdArgs) {
				search(cmd.Subcommands)
				continue
			}

			// Check primary command name
			if slices.Equal(cmdArgs[:len(cmd.Name)], cmd.Name) {
				if bestMatch == nil || len(cmd.Name) > len(bestMatch.path) {
					bestMatch = &commandEntry{path: cmd.Name, command: cmd}
				}
			}

			// Check aliases
			for _, alias := range cmd.Aliases {
				if alias == "" {
					continue
				}
				aliasPath := slices.Clone(cmd.Name)
				aliasPath[len(aliasPath)-1] = alias
				if len(aliasPath) <= len(cmdArgs) && slices.Equal(cmdArgs[:len(aliasPath)], aliasPath) {
					if bestMatch == nil || len(aliasPath) > len(bestMatch.path) {
						bestMatch = &commandEntry{path: aliasPath, command: cmd}
					}
				}
			}

			search(cmd.Subcommands)
		}
	}
	search(commands)

	return bestMatch
}

// parseFlags extracts flag names from args based on the provided flag definitions.
// It uses pflag for robust POSIX-compliant parsing of long flags (--flag),
// short flags (-f), combined short flags (-vq), and flags with values.
func parseFlags(args []string, flags []Flag) []string {
	fs := pflag.NewFlagSet("", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)

	for _, f := range flags {
		if f.Name == "" {
			continue
		}
		switch strings.ToLower(f.Type) {
		case "bool":
			fs.BoolP(f.Name, f.Shorthand, false, "")
		case "int":
			fs.IntP(f.Name, f.Shorthand, 0, "")
		case "intarray":
			fs.IntSliceP(f.Name, f.Shorthand, nil, "")
		case "stringarray":
			fs.StringSliceP(f.Name, f.Shorthand, nil, "")
		default: // string and any unknown types
			fs.StringP(f.Name, f.Shorthand, "", "")
		}
	}

	_ = fs.Parse(args)

	var result []string
	fs.Visit(func(f *pflag.Flag) {
		result = append(result, f.Name)
	})

	if len(result) == 0 {
		return nil
	}
	return result
}

// extractCommandArgs returns the leading non-flag args, stopping at "--" or any flag.
func extractCommandArgs(args []string) []string {
	for i, arg := range args {
		if arg == "--" || strings.HasPrefix(arg, "-") {
			return args[:i]
		}
	}
	return args
}
