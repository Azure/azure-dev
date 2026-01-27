// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"log"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// SpecBuilder builds Fig autocomplete specifications from Cobra commands.
// Note: This is different from Fig's concept of "generators" which are dynamic completion functions.
type SpecBuilder struct {
	suggestionProvider        CustomSuggestionProvider
	generatorProvider         CustomGeneratorProvider
	argsProvider              CustomArgsProvider
	flagArgsProvider          CustomFlagArgsProvider
	extensionMetadataProvider ExtensionMetadataProvider
	includeHidden             bool
}

// NewSpecBuilder creates a new Fig spec builder
func NewSpecBuilder(includeHidden bool) *SpecBuilder {
	azd := &Customizations{}
	return &SpecBuilder{
		suggestionProvider: azd,
		generatorProvider:  azd,
		argsProvider:       azd,
		flagArgsProvider:   azd,
		includeHidden:      includeHidden,
	}
}

// WithExtensionMetadata sets the extension metadata provider for the builder.
// When set, the builder will use extension metadata to generate full command trees
// for extensions that have the metadata capability.
func (sb *SpecBuilder) WithExtensionMetadata(provider ExtensionMetadataProvider) *SpecBuilder {
	sb.extensionMetadataProvider = provider
	return sb
}

// generateNonPersistentGlobalOptions generates options for non-persistent global flags (--help, --docs)
func (sb *SpecBuilder) generateNonPersistentGlobalOptions(root *cobra.Command) []Option {
	// Create a flagset with only the non-persistent global flags
	flagSet := pflag.NewFlagSet("", pflag.ContinueOnError)
	root.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		if slices.Contains(cmd.NonPersistentGlobalFlags, f.Name) {
			flagSet.AddFlag(f)
		}
	})
	return sb.generateOptions(flagSet, "", true)
}

// BuildSpec generates a Fig spec from a Cobra root command
func (sb *SpecBuilder) BuildSpec(root *cobra.Command) *Spec {
	persistentOpts := sb.generateOptions(root.PersistentFlags(), "", true)

	// Include non-persistent global flags (--help, --docs) as persistent since they appear on all commands
	nonPersistentGlobalOpts := sb.generateNonPersistentGlobalOptions(root)
	persistentOpts = append(persistentOpts, nonPersistentGlobalOpts...)

	subcommands := sb.generateSubcommands(root, &CommandContext{
		Command:     root,
		CommandPath: root.Name(),
	})

	return &Spec{
		Name:        root.Name(),
		Description: "Azure Developer CLI",
		Subcommands: subcommands,
		Options:     persistentOpts,
	}
}

func (sb *SpecBuilder) generateSubcommands(cmd *cobra.Command, ctx *CommandContext) []Subcommand {
	var subcommands []Subcommand

	for _, sub := range cmd.Commands() {
		if !sb.includeHidden && sub.Hidden {
			continue
		}

		if sub.Name() == "help" {
			continue // Added separately at root level
		}

		subCtx := &CommandContext{
			Command:     sub,
			CommandPath: ctx.CommandPath + " " + sub.Name(),
			Parent:      ctx,
		}

		names := []string{sub.Name()}
		names = append(names, sub.Aliases...)

		// Check if this is an extension command with metadata capability
		if extensionSubcmd := sb.tryGenerateExtensionSubcommand(sub, names); extensionSubcmd != nil {
			subcommands = append(subcommands, *extensionSubcmd)
			continue
		}

		localOpts := sb.generateOptions(sub.LocalNonPersistentFlags(), subCtx.CommandPath, false)
		args := sb.generateCommandArgs(sub, subCtx)
		nestedSubcommands := sb.generateSubcommands(sub, subCtx)

		subcommands = append(subcommands, Subcommand{
			Name:        names,
			Description: sub.Short,
			Subcommands: nestedSubcommands,
			Options:     localOpts,
			Args:        args,
			Hidden:      sub.Hidden,
		})
	}

	if ctx.Command == ctx.Command.Root() {
		subcommands = append(subcommands, sb.generateHelpCommand(cmd))
	}

	return subcommands
}

// tryGenerateExtensionSubcommand attempts to generate a subcommand from extension metadata.
// Returns nil if the command is not an extension command or has no metadata available.
func (sb *SpecBuilder) tryGenerateExtensionSubcommand(cmd *cobra.Command, names []string) *Subcommand {
	if sb.extensionMetadataProvider == nil {
		return nil
	}

	// Check if this command has extension annotations
	extensionId, hasId := cmd.Annotations["extension.id"]
	if !hasId {
		return nil
	}

	// Check if extension has metadata capability before attempting to load
	if !sb.extensionMetadataProvider.HasMetadataCapability(extensionId) {
		return nil
	}

	// Try to load extension metadata
	metadata, err := sb.extensionMetadataProvider.LoadMetadata(extensionId)
	if err != nil {
		log.Printf("Failed to load metadata for extension '%s': %v", extensionId, err)
		return nil
	}
	if metadata == nil {
		return nil
	}

	// Build the subcommand from metadata
	subcommand := &Subcommand{
		Name:        names,
		Description: cmd.Short,
		Hidden:      cmd.Hidden,
	}

	// Add subcommands from metadata
	for _, extCmd := range metadata.Commands {
		figSubcmd := convertExtensionCommand(extCmd, sb.includeHidden)
		if figSubcmd != nil {
			subcommand.Subcommands = append(subcommand.Subcommands, *figSubcmd)
		}
	}

	return subcommand
}

func (sb *SpecBuilder) generateOptions(flagSet *pflag.FlagSet, commandPath string, persistent bool) []Option {
	var options []Option
	flagSet.VisitAll(func(flag *pflag.Flag) {
		if !sb.includeHidden && flag.Hidden {
			return
		}

		// Skip non-persistent global flags for subcommands since they're already defined as persistent at root
		if !persistent && slices.Contains(cmd.NonPersistentGlobalFlags, flag.Name) {
			return
		}

		names := []string{"--" + flag.Name}
		if flag.Shorthand != "" {
			names = append(names, "-"+flag.Shorthand)
		}

		isRepeatable := strings.Contains(flag.Value.Type(), "Slice") ||
			strings.Contains(flag.Value.Type(), "Array")

		// Handle flags that are marked as `cmd.MarkFlagRequired`
		isRequired := false
		if annotations := flag.Annotations[cobra.BashCompOneRequiredFlag]; len(annotations) > 0 {
			isRequired = annotations[0] == "true"
		}

		isDangerous := flag.Name == "force" ||
			flag.Name == "purge" ||
			flag.Name == "show-secrets"

		flagCtx := &FlagContext{
			Flag:        flag,
			CommandPath: commandPath,
		}

		args := sb.generateFlagArgs(flag, flagCtx)

		options = append(options, Option{
			Name:         names,
			Description:  flag.Usage,
			Args:         args,
			IsPersistent: persistent,
			IsRepeatable: isRepeatable,
			IsRequired:   isRequired,
			IsDangerous:  isDangerous,
			Hidden:       flag.Hidden,
		})
	})

	return options
}

func (sb *SpecBuilder) generateFlagArgs(flag *pflag.Flag, ctx *FlagContext) []Arg {
	if flag.Value.Type() == "bool" || flag.Value.Type() == "" {
		return nil
	}

	arg := Arg{Name: flag.Name}

	// Apply customizations (name, description)
	if sb.flagArgsProvider != nil {
		if customArg := sb.flagArgsProvider.GetFlagArgs(ctx); customArg != nil {
			arg = *customArg
		}
	}

	// Apply static suggestions
	if sb.suggestionProvider != nil {
		if suggestions := sb.suggestionProvider.GetSuggestions(ctx); len(suggestions) > 0 {
			arg.Suggestions = suggestions
		}
	}

	// Apply dynamic generator
	if sb.generatorProvider != nil {
		if generator := sb.generatorProvider.GetFlagGenerator(ctx); generator != "" {
			arg.Generator = generator
		}
	}

	return []Arg{arg}
}

func (sb *SpecBuilder) generateCommandArgs(cmd *cobra.Command, ctx *CommandContext) []Arg {
	// Use custom args if provided
	if sb.argsProvider != nil {
		if customArgs := sb.argsProvider.GetCommandArgs(ctx); customArgs != nil {
			if sb.generatorProvider != nil {
				for i := range customArgs {
					if generator := sb.generatorProvider.GetCommandArgGenerator(ctx, customArgs[i].Name); generator != "" {
						customArgs[i].Generator = generator
					}
				}
			}
			return customArgs
		}
	}

	// Otherwise parse from command's Use field (e.g., "command [arg1] <arg2>")
	useParts := strings.Fields(cmd.Use)
	if len(useParts) <= 1 {
		return nil
	}

	args := make([]Arg, 0, len(useParts)-1)
	for _, part := range useParts[1:] {
		isOptional := strings.HasPrefix(part, "[") && strings.HasSuffix(part, "]")
		argName := strings.Trim(part, "[]<>")

		if strings.HasPrefix(argName, "-") {
			continue // Skip flags
		}

		arg := Arg{
			Name:       argName,
			IsOptional: isOptional,
		}

		if sb.generatorProvider != nil {
			if generator := sb.generatorProvider.GetCommandArgGenerator(ctx, argName); generator != "" {
				arg.Generator = generator
			}
		}

		args = append(args, arg)
	}

	return args
}

func (sb *SpecBuilder) generateHelpCommand(root *cobra.Command) Subcommand {
	return Subcommand{
		Name:        []string{"help"},
		Description: "Help about any command",
		Subcommands: sb.generateHelpSubcommands(root),
	}
}

func (sb *SpecBuilder) generateHelpSubcommands(cmd *cobra.Command) []Subcommand {
	var subcommands []Subcommand

	for _, sub := range cmd.Commands() {
		if !sb.includeHidden && sub.Hidden {
			continue
		}

		if sub.Name() == "help" {
			continue
		}

		names := []string{sub.Name()}
		names = append(names, sub.Aliases...)

		// Check if this is an extension command with metadata
		if helpSubcmd := sb.tryGenerateExtensionHelpSubcommand(sub, names); helpSubcmd != nil {
			subcommands = append(subcommands, *helpSubcmd)
			continue
		}

		helpSub := Subcommand{
			Name:        names,
			Description: sub.Short,
			Subcommands: sb.generateHelpSubcommands(sub),
		}

		subcommands = append(subcommands, helpSub)
	}

	return subcommands
}

// tryGenerateExtensionHelpSubcommand attempts to generate a help subcommand from extension metadata.
func (sb *SpecBuilder) tryGenerateExtensionHelpSubcommand(cmd *cobra.Command, names []string) *Subcommand {
	if sb.extensionMetadataProvider == nil {
		return nil
	}

	extensionId, hasId := cmd.Annotations["extension.id"]
	if !hasId {
		return nil
	}

	// Check if extension has metadata capability before attempting to load
	if !sb.extensionMetadataProvider.HasMetadataCapability(extensionId) {
		return nil
	}

	metadata, err := sb.extensionMetadataProvider.LoadMetadata(extensionId)
	if err != nil {
		log.Printf("Failed to load metadata for extension '%s': %v", extensionId, err)
		return nil
	}
	if metadata == nil {
		return nil
	}

	subcommand := &Subcommand{
		Name:        names,
		Description: cmd.Short,
	}

	// Add help subcommands from metadata
	for _, extCmd := range metadata.Commands {
		helpSubcmd := convertExtensionCommandForHelp(extCmd, sb.includeHidden)
		if helpSubcmd != nil {
			subcommand.Subcommands = append(subcommand.Subcommands, *helpSubcmd)
		}
	}

	return subcommand
}
