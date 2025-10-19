// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Generator generates Fig autocomplete specifications from Cobra commands
type Generator struct {
	suggestionProvider CustomSuggestionProvider
	generatorProvider  CustomGeneratorProvider
	argsProvider       CustomArgsProvider
	flagArgsProvider   CustomFlagArgsProvider
	includeHidden      bool
}

// NewGenerator creates a new Fig spec generator
func NewGenerator(includeHidden bool) *Generator {
	azd := &AzdCustomizations{}
	return &Generator{
		suggestionProvider: azd,
		generatorProvider:  azd,
		argsProvider:       azd,
		flagArgsProvider:   azd,
		includeHidden:      includeHidden,
	}
}

// GenerateSpec generates a Fig spec from a Cobra root command
func (g *Generator) GenerateSpec(root *cobra.Command) *Spec {
	// Generate root level persistent options
	persistentOpts := g.generateOptions(root.PersistentFlags(), "", true)

	// Also include root-level local flags that appear on all commands (--help, --docs)
	// These are treated as persistent for Fig spec purposes
	localOpts := g.generateOptions(root.LocalNonPersistentFlags(), "", true)
	persistentOpts = append(persistentOpts, localOpts...)

	// Generate subcommands
	subcommands := g.generateSubcommands(root, &CommandContext{
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

// generateSubcommands generates subcommands for a given command
func (g *Generator) generateSubcommands(cmd *cobra.Command, ctx *CommandContext) []Subcommand {
	var subcommands []Subcommand

	for _, sub := range cmd.Commands() {
		// Skip hidden commands unless includeHidden is true
		if !g.includeHidden && sub.Hidden {
			continue
		}

		// Skip the help command (we'll add it separately)
		if sub.Name() == "help" {
			continue
		}

		subCtx := &CommandContext{
			Command:     sub,
			CommandPath: ctx.CommandPath + " " + sub.Name(),
			Parent:      ctx,
		}

		// Get names (command + aliases)
		names := []string{sub.Name()}
		names = append(names, sub.Aliases...)

		// Generate options for this subcommand
		localOpts := g.generateOptions(sub.LocalNonPersistentFlags(), subCtx.CommandPath, false)

		// Generate args if any
		args := g.generateCommandArgs(sub, subCtx)

		// Recursively generate subcommands
		nestedSubcommands := g.generateSubcommands(sub, subCtx)

		subcommands = append(subcommands, Subcommand{
			Name:        names,
			Description: sub.Short,
			Subcommands: nestedSubcommands,
			Options:     localOpts,
			Args:        args,
			Hidden:      sub.Hidden,
		})
	}

	// Add help subcommand
	if ctx.Command == ctx.Command.Root() {
		subcommands = append(subcommands, g.generateHelpCommand(cmd))
	}

	return subcommands
}

// generateOptions generates Fig options from pflags
func (g *Generator) generateOptions(flagSet *pflag.FlagSet, commandPath string, persistent bool) []Option {
	var options []Option

	flagSet.VisitAll(func(flag *pflag.Flag) {
		// Skip hidden flags unless includeHidden is true
		if !g.includeHidden && flag.Hidden {
			return
		}

		// Skip global persistent flags when generating local flags (they're defined at root)
		if !persistent && ShouldSkipPersistentFlag(flag) {
			return
		}

		// Build names array (long form + short form)
		names := []string{"--" + flag.Name}
		if flag.Shorthand != "" {
			names = append(names, "-"+flag.Shorthand)
		}

		// Check if flag is repeatable
		isRepeatable := strings.Contains(flag.Value.Type(), "Slice") ||
			strings.Contains(flag.Value.Type(), "Array")

		// Check if flag is required
		isRequired := false
		if annotations := flag.Annotations["cobra_annotation_bash_completion_one_required_flag"]; len(annotations) > 0 {
			isRequired = annotations[0] == "true"
		}

		// Check if flag is dangerous
		isDangerous := flag.Name == "force" ||
			flag.Name == "purge" ||
			flag.Name == "show-secrets"

		// Generate args for the flag
		flagCtx := &FlagContext{
			Flag:        flag,
			CommandPath: commandPath,
		}

		args := g.generateFlagArgs(flag, flagCtx)

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

// generateFlagArgs generates arguments for a flag
func (g *Generator) generateFlagArgs(flag *pflag.Flag, ctx *FlagContext) []Arg {
	// Bool flags don't have arguments
	if flag.Value.Type() == "bool" || flag.Value.Type() == "boolptr" {
		return nil
	}

	var args []Arg

	// Check for custom flag args first (provides custom name and description)
	var arg Arg
	if g.flagArgsProvider != nil {
		if customArg := g.flagArgsProvider.GetFlagArgs(ctx); customArg != nil {
			arg = *customArg
		} else {
			// Get the argument name (use flag name as default)
			arg = Arg{
				Name: flag.Name,
			}
		}
	} else {
		// Get the argument name (use flag name as default)
		arg = Arg{
			Name: flag.Name,
		}
	}

	// Check for custom suggestions
	if g.suggestionProvider != nil {
		if suggestions := g.suggestionProvider.GetSuggestions(ctx); len(suggestions) > 0 {
			arg.Suggestions = suggestions
		}
	}

	// Check for custom generator
	if g.generatorProvider != nil {
		if generator := g.generatorProvider.GetFlagGenerator(ctx); generator != "" {
			arg.Generator = generator
		}
	}

	args = append(args, arg)
	return args
}

// generateCommandArgs generates positional arguments for a command
func (g *Generator) generateCommandArgs(cmd *cobra.Command, ctx *CommandContext) []Arg {
	var args []Arg

	// Check for custom args first
	if g.argsProvider != nil {
		if customArgs := g.argsProvider.GetCommandArgs(ctx); customArgs != nil {
			args = customArgs
		}
	}

	// If no custom args, parse the Use field to extract argument information
	if args == nil {
		useParts := strings.Fields(cmd.Use)
		if len(useParts) > 1 {
			for _, part := range useParts[1:] {
				// Check if argument is optional (enclosed in [])
				isOptional := strings.HasPrefix(part, "[") && strings.HasSuffix(part, "]")
				argName := strings.Trim(part, "[]<>")

				// Skip flag-like parts
				if strings.HasPrefix(argName, "-") {
					continue
				}

				arg := Arg{
					Name:       argName,
					IsOptional: isOptional,
				}

				args = append(args, arg)
			}
		}
	}

	// Apply generators to all args (whether custom or parsed)
	if g.generatorProvider != nil {
		for i := range args {
			if generator := g.generatorProvider.GetCommandArgGenerator(ctx, args[i].Name); generator != "" {
				args[i].Generator = generator
			}
		}
	}

	return args
}

// generateHelpCommand generates the help subcommand with nested structure
func (g *Generator) generateHelpCommand(root *cobra.Command) Subcommand {
	helpCmd := Subcommand{
		Name:        []string{"help"},
		Description: "Help about any command",
		Subcommands: g.generateHelpSubcommands(root),
	}

	return helpCmd
}

// generateHelpSubcommands generates help subcommands recursively
func (g *Generator) generateHelpSubcommands(cmd *cobra.Command) []Subcommand {
	var subcommands []Subcommand

	for _, sub := range cmd.Commands() {
		// Skip hidden commands in help
		if sub.Hidden {
			continue
		}

		// Skip the help command itself and generate-fig-spec
		if sub.Name() == "help" {
			continue
		}

		// Get names (command + aliases)
		names := []string{sub.Name()}
		names = append(names, sub.Aliases...)

		helpSub := Subcommand{
			Name:        names,
			Description: sub.Short,
			Subcommands: g.generateHelpSubcommands(sub),
		}

		subcommands = append(subcommands, helpSub)
	}

	return subcommands
}
