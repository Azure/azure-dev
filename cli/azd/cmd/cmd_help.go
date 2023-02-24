// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	cmdGrouper      string = "commandGrouper"
	cmdGroupConfig  string = string(i18nCmdGroupTitleConfig)
	cmdGroupManage  string = string(i18nCmdGroupTitleManage)
	cmdGroupMonitor string = string(i18nCmdGroupTitleMonitor)
	cmdGroupAbout   string = string(i18nCmdGroupTitleAbout)
	// this is used for aligning titles in the console.
	endOfTitleSentinel string = "\x00"
)

func annotateGroupCmd(cmd *cobra.Command, group string) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[cmdGrouper] = group
}

type cmdHelpGenerator func(cmd *cobra.Command) string

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
  - description: Describes the command. It is usually linked to the cobra `Short or Long` fields.
  - usage: Demonstrate how to call the command. The function `getCmdHelpUsage` can be used to easily set
    text from an i18TextId.
  - commands: The list of sub-commands supported. Use function `getRootCommandsDetails(cobraCommand)` to auto generate
    the list of sub-commands from the cobra command. This section is optional and won't be displayed when there are not
    sub-commands added for the command.
  - flags: Similar to commands, use function `getCmdHelpFlags(cobraCommand)` to auto-generate the list of local and global
    flags.
  - footer: The last section is where commands can define quick-start, examples or extra notes.

By using `cmdHelpGenerator` the implementation to format the content of the help can be changed, for example, to produce md
files for static help.
*/
func generateCmdHelp(
	cmd *cobra.Command,
	description cmdHelpGenerator,
	usage cmdHelpGenerator,
	commands cmdHelpGenerator,
	flags cmdHelpGenerator,
	footer cmdHelpGenerator) string {
	return fmt.Sprintf("\n%s%s%s%s%s\n",
		description(cmd),
		usage(cmd),
		commands(cmd),
		flags(cmd),
		footer(cmd),
	)
}

func getCmdHelpDefaultUsage(cmd *cobra.Command) string {
	return fmt.Sprintf("%s\n  %s\n\n",
		output.WithBold(output.WithUnderline(i18nGetText(i18nUsage))),
		"{{if .Runnable}}{{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}{{.CommandPath}} [command]{{end}}",
	)
}

func getCmdHelpDefaultDescription(cmd *cobra.Command) string {
	return formatHelpDescription(cmd.Short, nil)
}

func getCmdHelpDefaultCommands(cmd *cobra.Command) string {
	return getCmdHelpAvailableCommands(getCommandsDetails(cmd))
}

func getCmdHelpDefaultFooter(*cobra.Command) string {
	return generateHelpFindFillBug()
}

func getCmdHelpCommands(title i18nTextId, commands string) string {
	if commands == "" {
		return commands
	}
	return fmt.Sprintf("%s\n%s\n", output.WithBold(output.WithUnderline(i18nGetText(title))), commands)
}

func getCmdHelpGroupedCommands(commands string) string {
	return getCmdHelpCommands(i18nCommands, commands)
}

func getCmdHelpAvailableCommands(commands string) string {
	return getCmdHelpCommands(i18nAvailableCommands, commands)
}

func getCmdHelpDefaultFlags(cmd *cobra.Command) (result string) {
	if cmd.HasAvailableLocalFlags() {
		flags := getFlagsDetails(cmd.LocalFlags())
		result = fmt.Sprintf("%s\n%s\n",
			output.WithBold(output.WithUnderline(i18nGetText(i18nFlags))),
			flags)
	}
	if cmd.HasAvailableInheritedFlags() {
		globalFlags := getFlagsDetails(cmd.InheritedFlags())
		result += fmt.Sprintf("%s\n%s\n",
			output.WithBold(output.WithUnderline(i18nGetText(i18nGlobalFlags))),
			globalFlags)
	}
	return result
}

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

func alignTitles(lines []string, longestLineLen int) {
	for i, line := range lines {
		sentinelIndex := strings.Index(line, endOfTitleSentinel)
		// calculate the difference between the longest line to each line ending. It's 0 for the longest
		gapToFill := strings.Repeat(" ", longestLineLen-sentinelIndex)
		lines[i] = fmt.Sprintf("%s%s\t: %s", line[:sentinelIndex], gapToFill, line[sentinelIndex+1:])
	}
}

func getCommandsDetails(cmd *cobra.Command) (result string) {
	childrenCommands := cmd.Commands()
	if len(childrenCommands) == 0 {
		return ""
	}

	// stores the longes line len
	max := 0
	var lines []string
	for _, childCommand := range childrenCommands {
		commandName := fmt.Sprintf("  %s", childCommand.Name())
		commandNameLen := len(commandName)
		if commandNameLen > max {
			max = commandNameLen
		}
		lines = append(lines,
			fmt.Sprintf("%s%s%s", commandName, endOfTitleSentinel, childCommand.Short))
	}
	// align all lines
	alignTitles(lines, max)
	return fmt.Sprintf("%s\n", strings.Join(lines, "\n"))
}

func formatHelpNote(note string) string {
	return fmt.Sprintf("  • %s", note)
}

func getCommonFooterNote(command string) string {
	addSpace := ""
	if len(command) > 0 {
		addSpace = " "
	}
	return fmt.Sprintf("%s\n", i18nGetTextWithConfig(&i18n.LocalizeConfig{
		MessageID: string(i18nCmdCommonFooter),
		TemplateData: struct {
			AzdRun string
		}{
			AzdRun: fmt.Sprintf("%s %s %s",
				output.WithHighLightFormat("azd%s%s", addSpace, command),
				output.WithWarningFormat("[command]"),
				output.WithHighLightFormat("--help"),
			),
		},
	}))
}

func formatHelpDescription(title string, notes []string) string {
	var note string
	if len(notes) > 0 {
		note = fmt.Sprintf("%s\n\n", strings.Join(notes, "\n"))
	}
	return fmt.Sprintf("%s\n\n%s", title, note)
}

func generateHelpFindFillBug() string {
	return fmt.Sprintf("%s %s.\n",
		i18nGetText(i18nCmdRootHelpFooterReportBug),
		output.WithLinkFormat(i18nGetText(i18nAzdHats)))
}

func getCmdHelpSample(description, code string) string {
	return fmt.Sprintf("  %s\n    %s", description, code)
}

func getCmdHelpSamplesBlock(samples []string) string {
	return fmt.Sprintf("%s\n%s\n",
		output.WithBold(output.WithUnderline("%s", i18nGetText(i18nExamples))),
		strings.Join(samples, "\n\n"),
	)
}
