// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	// this is used for aligning titles in the console.
	endOfTitleSentinel string = "\x00"
)

// cmdHelpGenerator defines the required signature to implement and produce help description for commands.
type cmdHelpGenerator func(cmd *cobra.Command) string

// generateCmdHelpOptions defines settings to control the text description for displaying the commands' help.
type generateCmdHelpOptions struct {
	Description cmdHelpGenerator
	Usage       cmdHelpGenerator
	Commands    cmdHelpGenerator
	Flags       cmdHelpGenerator
	Footer      cmdHelpGenerator
}

/*
generateCmdHelp sets the base structure for displaying help documentation for a command in the console.
The base structure is on the form of:

**********************

<description>

<usage>
<commands>
<flags>
<footer>

**********************

Where:
  - description: Main information for the command. Default to cobra's `Short` field.
  - usage: Demonstrate how to call the command. Default to cobra's `Use` filed.
  - commands: The list of sub-commands supported. Default to list cobra's sub-commands in the form of `cmd : short-notes`.
  - flags: List of supported flags. Default to `Flags + Global flags` and each flag as `-F, --flag [type] : description`.
  - footer: The last section is where commands can define quick-start, examples or extra notes. Default to display notes
    about how to report bugs or comments.
*/
func generateCmdHelp(
	cmd *cobra.Command,
	options generateCmdHelpOptions) string {

	getGeneratorOrDefault := func(option, defaultOption cmdHelpGenerator) cmdHelpGenerator {
		if option != nil {
			return option
		}
		return defaultOption
	}

	return fmt.Sprintf("\n%s%s%s%s%s%s\n",
		getGeneratorOrDefault(options.Description, getCmdHelpDefaultDescription)(cmd),
		getGeneratorOrDefault(options.Usage, getCmdHelpDefaultUsage)(cmd),
		getGeneratorOrDefault(options.Commands, getCmdHelpDefaultCommands)(cmd),
		getGeneratorOrDefault(options.Flags, getCmdHelpDefaultFlags)(cmd),
		getPreFooter(cmd),
		getGeneratorOrDefault(options.Footer, getCmdHelpDefaultFooter)(cmd),
	)
}

// getCmdHelpDefaultDescription provides the default implementation for displaying the help description section.
func getCmdHelpDefaultDescription(cmd *cobra.Command) string {
	return generateCmdHelpDescription(cmd.Short, nil)
}

// getCmdHelpDefaultUsage provides the default implementation for displaying the help usage section.
func getCmdHelpDefaultUsage(cmd *cobra.Command) string {
	return fmt.Sprintf("%s\n  %s\n\n",
		output.WithBold("%s", output.WithUnderline("Usage")),
		"{{if .Runnable}}{{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}{{.CommandPath}} [command]{{end}}",
	)
}

// getCmdHelpDefaultCommands provides the default implementation for displaying the help commands section.
func getCmdHelpDefaultCommands(cmd *cobra.Command) string {
	return getCmdHelpAvailableCommands(getCommandsDetails(cmd))
}

// getCmdHelpDefaultFlags provides the default implementation for displaying the help flags section.
func getCmdHelpDefaultFlags(cmd *cobra.Command) (result string) {
	// force the following flags as global flags for display purposes when displaying help.
	forceGlobalFlagNames := map[string]struct{}{
		"help": {},
		"docs": {},
	}

	forceGlobalFlags := pflag.NewFlagSet("", pflag.ContinueOnError)
	localFlags := pflag.NewFlagSet("", pflag.ContinueOnError)

	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if _, ok := forceGlobalFlagNames[f.Name]; ok {
			forceGlobalFlags.AddFlag(f)
		} else {
			localFlags.AddFlag(f)
		}
	})

	if localFlags.HasAvailableFlags() {
		details := getFlagsDetails(localFlags)
		result = fmt.Sprintf("%s\n%s\n",
			output.WithBold("%s", output.WithUnderline("Flags")),
			details)
	}

	globalFlags := pflag.NewFlagSet("", pflag.ContinueOnError)
	globalFlags.AddFlagSet(cmd.InheritedFlags())
	globalFlags.AddFlagSet(forceGlobalFlags)

	if globalFlags.HasAvailableFlags() {
		details := getFlagsDetails(globalFlags)
		result += fmt.Sprintf("%s\n%s\n",
			output.WithBold("%s", output.WithUnderline("Global Flags")),
			details)
	}
	return result
}

// getCmdHelpDefaultFooter provides the default implementation for displaying the help footer section.
func getCmdHelpDefaultFooter(*cobra.Command) string {
	return fmt.Sprintf("Find a bug? Want to let us know how we're doing? Fill out this brief survey: %s.\n",
		output.WithLinkFormat("https://aka.ms/azure-dev/hats"))
}

/*
getCmdHelpCommands defines the base structure for the commands section within the help as:
*******************
Commands:

{{ commands - description }}

*******************
*/
func getCmdHelpCommands(title string, commands string) string {
	if commands == "" {
		return commands
	}
	return fmt.Sprintf("%s\n%s\n", output.WithBold("%s", output.WithUnderline("%s", title)), commands)
}

// getCmdHelpGroupedCommands generates {{ commands - description }} where sub-commands are grouped.
func getCmdHelpGroupedCommands(commands string) string {
	return getCmdHelpCommands("Commands", commands)
}

// getCmdHelpAvailableCommands generates {{ commands - description }} for all sub-commands.
func getCmdHelpAvailableCommands(commands string) string {
	return getCmdHelpCommands("Available Commands", commands)
}

// getFlagsDetails produces the command - flags - details in the form of `-F, --flag [type] : description`
func getFlagsDetails(flagSet *pflag.FlagSet) (result string) {
	var lines []string
	max := 0
	flagSet.VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden {
			return
		}

		line := ""
		if flag.Shorthand != "" && flag.ShorthandDeprecated == "" {
			line = fmt.Sprintf("  -%s, --%s", flag.Shorthand, flag.Name)
		} else {
			line = fmt.Sprintf("      --%s", flag.Name)
		}

		varName, usage := pflag.UnquoteUsage(flag)
		if varName != "" {
			line += " " + varName
		}

		// insert a sentinel for the end of the flag titles. Lines are aligned based on the longest line.
		line += endOfTitleSentinel
		lineLen := len(line)
		if lineLen > max {
			// the max value is used later to fill all lines with same size
			max = lineLen
		}
		line += usage
		if len(flag.Deprecated) != 0 {
			line += fmt.Sprintf(" (DEPRECATED: %s)", flag.Deprecated)
		}

		lines = append(lines, line)
	})
	// no flags
	if max == 0 {
		return result
	}
	alignTitles(lines, max)
	return fmt.Sprintf("  %s\n", strings.Join(lines, "\n  "))
}

// alignTitles update all the input lines to be the same len by adding white spaces. Then it produces the `title : note`
// output.
// Note: alignTitles depends on all lines containing the `endOfTitleSentinel` which indicates the end of the title and where
// the colon is expected to be added after the aligning.
// Example:
/*
   input: [
		"title:foo",
		"titleTwo:foo",
		"titleTree:foo",
   ]
    result: [
		"title     : foo",
		"titleTwo  : foo",
		"titleTree : foo",
   ]

*/
func alignTitles(lines []string, longestLineLen int) {
	for i, line := range lines {
		sentinelIndex := strings.Index(line, endOfTitleSentinel)
		// calculate the difference between the longest line to each line ending. It's 0 for the longest
		gapToFill := strings.Repeat(" ", longestLineLen-sentinelIndex)
		lines[i] = fmt.Sprintf("%s%s\t: %s", line[:sentinelIndex], gapToFill, line[sentinelIndex+1:])
	}
}

// getCommandsDetails produces the default help - commands - description for any command in the form of `cmd : notes`.
func getCommandsDetails(cmd *cobra.Command) (result string) {
	childrenCommands := cmd.Commands()
	if len(childrenCommands) == 0 {
		return ""
	}

	// stores the longes line len
	max := 0
	var lines []string
	for _, childCommand := range childrenCommands {
		if !childCommand.IsAvailableCommand() {
			continue
		}

		commandName := fmt.Sprintf("  %s", childCommand.Name())
		commandNameLen := len(commandName)
		if commandNameLen > max {
			max = commandNameLen
		}
		lines = append(lines,
			fmt.Sprintf("%s%s%s", commandName, endOfTitleSentinel, childCommand.Short))
	}
	alignTitles(lines, max)
	return fmt.Sprintf("%s\n", strings.Join(lines, "\n"))
}

// formatHelpNote provides the expected format in description notes using `•`.
func formatHelpNote(note string) string {
	return fmt.Sprintf("  • %s", note)
}

// getPreFooter automatically adds a message to any command containing sub-commands about how to get help for subcommands.
func getPreFooter(c *cobra.Command) string {
	if !c.HasSubCommands() {
		return ""
	}

	return fmt.Sprintf("Use %s to view examples and more information about a specific command.\n\n",
		fmt.Sprintf("%s %s %s",
			output.WithHighLightFormat("%s", c.CommandPath()),
			output.WithWarningFormat("[command]"),
			output.WithHighLightFormat("--help"),
		))
}

// generateCmdHelpDescription construct a help text block from a title and description notes.
func generateCmdHelpDescription(title string, notes []string) string {
	var note string
	if len(notes) > 0 {
		note = fmt.Sprintf("%s\n\n", strings.Join(notes, "\n"))
	}
	return fmt.Sprintf("%s\n\n%s", title, note)
}

// generateCmdHelpSamplesBlock converts the samples within the input `samples` to a help text block describing each sample
// title and the command to run it.
func generateCmdHelpSamplesBlock(samples map[string]string) string {
	SamplesCount := len(samples)
	if SamplesCount == 0 {
		return ""
	}
	var lines []string
	for title, command := range samples {
		lines = append(lines, fmt.Sprintf("  %s\n    %s", title, command))
	}
	// sorting lines to keep a deterministic output, as map[string]string is not ordered
	slices.Sort(lines)
	return fmt.Sprintf("%s\n%s\n",
		output.WithBold("%s", output.WithUnderline("Examples")),
		strings.Join(lines, "\n\n"),
	)
}
