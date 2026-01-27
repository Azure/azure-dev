// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
)

// ExtensionMetadataProvider provides extension metadata for generating figspec completions
type ExtensionMetadataProvider interface {
	// LoadMetadata loads the cached metadata for an extension by its ID
	LoadMetadata(extensionId string) (*extensions.ExtensionCommandMetadata, error)
}

// convertExtensionCommand converts an extension command to a Fig subcommand recursively
func convertExtensionCommand(cmd extensions.Command, includeHidden bool) *Subcommand {
	if !includeHidden && cmd.Hidden {
		return nil
	}

	names := []string{cmd.Name[len(cmd.Name)-1]}
	names = append(names, cmd.Aliases...)

	subcommand := &Subcommand{
		Name:        names,
		Description: cmd.Short,
		Hidden:      cmd.Hidden,
	}

	// Convert flags to options
	for _, flag := range cmd.Flags {
		if !includeHidden && flag.Hidden {
			continue
		}

		option := convertExtensionFlag(flag)
		subcommand.Options = append(subcommand.Options, option)
	}

	// Convert args
	for _, arg := range cmd.Args {
		figArg := convertExtensionArg(arg)
		subcommand.Args = append(subcommand.Args, figArg)
	}

	// Convert subcommands recursively
	for _, subcmd := range cmd.Subcommands {
		figSubcmd := convertExtensionCommand(subcmd, includeHidden)
		if figSubcmd != nil {
			subcommand.Subcommands = append(subcommand.Subcommands, *figSubcmd)
		}
	}

	return subcommand
}

// convertExtensionFlag converts an extension flag to a Fig option
func convertExtensionFlag(flag extensions.Flag) Option {
	names := []string{"--" + flag.Name}
	if flag.Shorthand != "" {
		names = append(names, "-"+flag.Shorthand)
	}

	option := Option{
		Name:        names,
		Description: flag.Description,
		IsRequired:  flag.Required,
		Hidden:      flag.Hidden,
	}

	// Set isDangerous for common dangerous flags
	isDangerous := flag.Name == "force" ||
		flag.Name == "purge" ||
		flag.Name == "show-secrets"
	option.IsDangerous = isDangerous

	// Handle flag arguments (non-bool flags need args)
	if flag.Type != "bool" && flag.Type != "" {
		arg := Arg{
			Name: flag.Name,
		}

		// Add valid values as suggestions
		if len(flag.ValidValues) > 0 {
			arg.Suggestions = flag.ValidValues
		}

		option.Args = []Arg{arg}
	}

	// Set isRepeatable for array types
	isRepeatable := flag.Type == "stringArray" || flag.Type == "intArray"
	option.IsRepeatable = isRepeatable

	return option
}

// convertExtensionArg converts an extension argument to a Fig arg
func convertExtensionArg(arg extensions.Argument) Arg {
	figArg := Arg{
		Name:        arg.Name,
		Description: arg.Description,
		IsOptional:  !arg.Required,
	}

	// Add valid values as suggestions
	if len(arg.ValidValues) > 0 {
		figArg.Suggestions = arg.ValidValues
	}

	return figArg
}

// convertExtensionCommandForHelp converts an extension command to a Fig help subcommand recursively
// (only includes name, description, and nested subcommands for help tree)
func convertExtensionCommandForHelp(cmd extensions.Command, includeHidden bool) *Subcommand {
	if !includeHidden && cmd.Hidden {
		return nil
	}

	names := []string{cmd.Name[len(cmd.Name)-1]}
	names = append(names, cmd.Aliases...)

	subcommand := &Subcommand{
		Name:        names,
		Description: cmd.Short,
	}

	// Convert subcommands recursively for help tree
	for _, subcmd := range cmd.Subcommands {
		figSubcmd := convertExtensionCommandForHelp(subcmd, includeHidden)
		if figSubcmd != nil {
			subcommand.Subcommands = append(subcommand.Subcommands, *figSubcmd)
		}
	}

	return subcommand
}
