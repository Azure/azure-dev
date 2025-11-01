// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// SpecBuilder builds Fig autocomplete specifications from Cobra commands.
// Note: This is different from Fig's concept of "generators" which are dynamic completion functions.
type SpecBuilder struct {
	suggestionProvider CustomSuggestionProvider
	generatorProvider  CustomGeneratorProvider
	argsProvider       CustomArgsProvider
	flagArgsProvider   CustomFlagArgsProvider
	includeHidden      bool
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

// BuildSpec generates a Fig spec from a Cobra root command
func (sb *SpecBuilder) BuildSpec(root *cobra.Command) *Spec {
	persistentOpts := sb.generateOptions(root.PersistentFlags(), "", true)

	// Include root-level local flags (--help, --docs) as persistent since they appear on all commands
	localOpts := sb.generateOptions(root.LocalNonPersistentFlags(), "", true)
	persistentOpts = append(persistentOpts, localOpts...)

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

func (sb *SpecBuilder) generateOptions(flagSet *pflag.FlagSet, commandPath string, persistent bool) []Option {
	var options []Option

	flagSet.VisitAll(func(flag *pflag.Flag) {
		if !sb.includeHidden && flag.Hidden {
			return
		}

		if !persistent && ShouldSkipPersistentFlag(flag) {
			return // Global flags already defined at root
		}

		names := []string{"--" + flag.Name}
		if flag.Shorthand != "" {
			names = append(names, "-"+flag.Shorthand)
		}

		isRepeatable := strings.Contains(flag.Value.Type(), "Slice") ||
			strings.Contains(flag.Value.Type(), "Array")

		isRequired := false
		if annotations := flag.Annotations["cobra_annotation_bash_completion_one_required_flag"]; len(annotations) > 0 {
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
		if sub.Hidden || sub.Name() == "help" {
			continue
		}

		names := []string{sub.Name()}
		names = append(names, sub.Aliases...)

		helpSub := Subcommand{
			Name:        names,
			Description: sub.Short,
			Subcommands: sb.generateHelpSubcommands(sub),
		}

		subcommands = append(subcommands, helpSub)
	}

	return subcommands
}
