// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
)

// ExtensionMetadataProvider provides extension metadata for generating figspec completions
type ExtensionMetadataProvider interface {
	// HasMetadataCapability checks if the extension has the metadata capability
	HasMetadataCapability(extensionId string) bool
	// LoadMetadata loads the cached metadata for an extension by its ID
	LoadMetadata(extensionId string) (*extensions.ExtensionCommandMetadata, error)
}

// convertExtensionCommand converts an extension command to a Fig subcommand recursively.
// globalFlagNames contains flag names that should be excluded (already defined at root level).
func convertExtensionCommand(extCmd extensions.Command, includeHidden bool, globalFlagNames map[string]bool) *Subcommand {
	if !includeHidden && extCmd.Hidden {
		return nil
	}

	if len(extCmd.Name) == 0 {
		return nil
	}

	names := append([]string{extCmd.Name[len(extCmd.Name)-1]}, extCmd.Aliases...)

	subcommand := &Subcommand{
		Name:        names,
		Description: extCmd.Short,
		Hidden:      extCmd.Hidden,
	}

	// Convert flags to options, excluding global flags which are already defined at root level
	for _, flag := range extCmd.Flags {
		if !includeHidden && flag.Hidden {
			continue
		}
		if globalFlagNames[flag.Name] {
			continue
		}

		option := convertExtensionFlag(flag)
		subcommand.Options = append(subcommand.Options, option)
	}

	// Convert args
	for _, arg := range extCmd.Args {
		figArg := convertExtensionArg(arg)
		subcommand.Args = append(subcommand.Args, figArg)
	}

	// Convert subcommands recursively
	for _, childCmd := range extCmd.Subcommands {
		figChild := convertExtensionCommand(childCmd, includeHidden, globalFlagNames)
		if figChild != nil {
			subcommand.Subcommands = append(subcommand.Subcommands, *figChild)
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

	if len(cmd.Name) == 0 {
		return nil
	}

	names := append([]string{cmd.Name[len(cmd.Name)-1]}, cmd.Aliases...)

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
