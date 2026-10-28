// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"io"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
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

// ResolveCommand returns the best matching command for given args.
func ResolveCommand(metadata *ExtensionCommandMetadata, args []string) *Command {
	if metadata == nil {
		return nil
	}

	match := matchCommand(metadata.Commands, args)
	if match == nil {
		return nil
	}

	return match.command
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

// matchCommand returns the longest command matching args.
// It matches names and aliases throughout the command tree.
// A matching entry always returns its canonical command path.
func matchCommand(commands []Command, args []string) *commandEntry {
	cmdArgs := extractCommandArgs(internal.StripGlobalFlags(args))
	if len(cmdArgs) == 0 {
		return nil
	}

	var bestMatch *commandEntry

	var search func([]Command, []string)
	search = func(cmds []Command, ancestorPath []string) {
		for i := range cmds {
			cmd := &cmds[i]
			if len(cmd.Name) == 0 || len(cmd.Name) > len(cmdArgs) {
				search(cmd.Subcommands, ancestorPath)
				continue
			}

			index := len(cmd.Name) - 1
			prefixStart := 0
			if len(ancestorPath) <= index && slices.Equal(cmd.Name[:len(ancestorPath)], ancestorPath) {
				// The ancestor already matched its aliased prefix.
				prefixStart = len(ancestorPath)
			}
			matchesPrefix := slices.Equal(cmdArgs[prefixStart:index], cmd.Name[prefixStart:index])
			name := cmdArgs[index]
			matchesName := name == cmd.Name[index] || (name != "" && slices.Contains(cmd.Aliases, name))
			nextAncestor := ancestorPath
			if matchesPrefix && matchesName {
				if bestMatch == nil || len(cmd.Name) > len(bestMatch.path) {
					bestMatch = &commandEntry{path: cmd.Name, command: cmd}
				}
				nextAncestor = cmd.Name
			}

			search(cmd.Subcommands, nextAncestor)
		}
	}
	search(commands, nil)

	return bestMatch
}

// parseFlags extracts flag names from args based on the provided flag definitions.
// It uses pflag for robust POSIX-compliant parsing of long flags (--flag),
// short flags (-f), combined short flags (-vq), and flags with values.
func parseFlags(args []string, flags []Flag) []string {
	fs := internal.NewGlobalFlagSet()
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	fs.StringP("output", "o", "", "")
	fs.BoolP("help", "h", false, "")
	fs.Bool("docs", false, "")

	for _, f := range flags {
		if f.Name == "" {
			continue
		}
		if fs.Lookup(f.Name) != nil {
			continue
		}
		// Legacy shorthands may overlap globals; keep their long names.
		if f.Shorthand != "" && fs.ShorthandLookup(f.Shorthand) != nil {
			f.Shorthand = ""
		}
		flagType := strings.ToLower(f.Type)
		switch flagType {
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

		if f.ValueOptional && flagType != "bool" {
			// The resolver only records flag presence, so any valid value can stand in for NoOptDefVal.
			noOptDefVal := "true"
			if flagType == "int" || flagType == "intarray" {
				noOptDefVal = "0"
			}
			fs.Lookup(f.Name).NoOptDefVal = noOptDefVal
		}
	}

	_ = fs.Parse(args)

	var result []string
	fs.Visit(func(f *pflag.Flag) {
		if slices.ContainsFunc(flags, func(definition Flag) bool {
			return definition.Name == f.Name
		}) {
			result = append(result, f.Name)
		}
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
