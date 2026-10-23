// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const cuUsageTemplate = `{{cuHelpHeading "Usage"}}
{{cuHelpUsage .}}
{{if .Aliases}}
{{cuHelpHeading "Aliases"}}
  {{.NameAndAliases}}
{{end}}{{if .HasAvailableSubCommands}}
{{cuHelpHeading "Available Commands"}}
{{cuHelpCommands .}}{{end}}{{with cuHelpLocalFlags .}}
{{cuHelpHeading "Flags"}}
{{.}}{{end}}{{with cuHelpGlobalFlags .}}
{{cuHelpHeading "Global Flags"}}
{{.}}{{end}}{{with .Example}}
{{cuHelpHeading "Examples"}}
{{cuHelpExamples .}}
{{end}}{{if .HasAvailableSubCommands}}
Use {{cuHelpText (printf "%s [command] --help" (cuHelpPath .))}} for more information about a command.
{{end}}`

var (
	registerHelpTemplates sync.Once
	helpTokenPattern      = regexp.MustCompile(
		"`azd[^`\\r\\n]+`|'azd[^'\\r\\n]+'|https?://[^\\s`'\"]+|" +
			`--[a-z][a-z0-9-]*|\B-[A-Za-z]\b|<[A-Za-z][^<>\r\n]*>|\[[A-Za-z][^\[\]\r\n]*\]`,
	)
	helpFlagPattern = regexp.MustCompile(`^( +)((?:-[A-Za-z0-9], )?--\S.*?)( {2,})(\S.*)$`)
)

func installHelp(root *cobra.Command) {
	registerHelpTemplates.Do(func() {
		cobra.AddTemplateFuncs(map[string]any{
			"cuHelpHeading":     helpHeading,
			"cuHelpText":        styleHelpText,
			"cuHelpPath":        helpCommandPath,
			"cuHelpUsage":       helpUsage,
			"cuHelpCommands":    helpCommands,
			"cuHelpLocalFlags":  func(cmd *cobra.Command) string { return helpFlags(cmd, false) },
			"cuHelpGlobalFlags": func(cmd *cobra.Command) string { return helpFlags(cmd, true) },
			"cuHelpExamples":    helpExamples,
			"cuHelpDescription": func(cmd *cobra.Command) string {
				text := cmd.Long
				if text == "" {
					text = cmd.Short
				}
				return styleHelpText(strings.TrimSpace(text)) + "\n\n"
			},
		})
	})
	root.SetHelpTemplate(`{{cuHelpDescription .}}{{.UsageString}}`)
	// Keep the SDK UsageFunc, which applies per-command global flag overrides.
	root.SetUsageTemplate(cuUsageTemplate)
}

func helpHeading(text string) string {
	return output.WithBold("%s", output.WithUnderline("%s", text))
}

func helpCommandPath(cmd *cobra.Command) string {
	return "azd ai " + cmd.CommandPath()
}

func helpUsage(cmd *cobra.Command) string {
	var lines []string
	if cmd.Runnable() {
		line := helpCommandPath(cmd) + strings.TrimPrefix(cmd.UseLine(), cmd.CommandPath())
		lines = append(lines, "  "+styleHelpText(line))
	}
	if cmd.HasAvailableSubCommands() {
		lines = append(lines, "  "+styleHelpText(helpCommandPath(cmd)+" [command]"))
	}
	return strings.Join(lines, "\n")
}

func helpCommands(cmd *cobra.Command) string {
	width := 0
	for _, child := range cmd.Commands() {
		if child.IsAvailableCommand() {
			width = max(width, len(child.Name()))
		}
	}
	var text strings.Builder
	for _, child := range cmd.Commands() {
		if child.IsAvailableCommand() {
			fmt.Fprintf(&text, "  %s%s : %s\n",
				output.WithHighLightFormat("%s", child.Name()),
				strings.Repeat(" ", width-len(child.Name())), styleHelpText(child.Short))
		}
	}
	return text.String()
}

func helpFlags(cmd *cobra.Command, global bool) string {
	selected := pflag.NewFlagSet("", pflag.ContinueOnError)
	if global {
		selected.AddFlagSet(cmd.InheritedFlags())
	}
	cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		isGlobal := flag.Name == "help" || flag.Name == "docs" || cmd.PersistentFlags().Lookup(flag.Name) != nil
		if isGlobal == global {
			selected.AddFlag(flag)
		}
	})
	lines := strings.Split(strings.TrimSuffix(selected.FlagUsages(), "\n"), "\n")
	width := 0
	for _, line := range lines {
		if parts := helpFlagPattern.FindStringSubmatch(line); parts != nil {
			width = max(width, len(parts[1])+len(parts[2]))
		}
	}
	for i, line := range lines {
		if parts := helpFlagPattern.FindStringSubmatch(line); parts != nil {
			padding := strings.Repeat(" ", width-len(parts[1])-len(parts[2]))
			lines[i] = parts[1] + styleHelpText(parts[2]) + padding + " : " + styleHelpText(parts[4])
		} else {
			lines[i] = styleHelpText(line)
		}
	}
	if text := strings.Join(lines, "\n"); text != "" {
		return text + "\n"
	}
	return ""
}

func helpExamples(text string) string {
	lines := strings.Split(text, "\n")
	continuation := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isCommand := strings.HasPrefix(trimmed, "azd ") || continuation
		continuation = isCommand && strings.HasSuffix(trimmed, "\\")
		if isCommand {
			lines[i] = output.WithHighLightFormat("%s", line)
		} else if trimmed != "" {
			lines[i] = output.WithGrayFormat("%s", line)
		}
	}
	return strings.Join(lines, "\n")
}

func styleHelpText(text string) string {
	return helpTokenPattern.ReplaceAllStringFunc(text, func(token string) string {
		switch {
		case strings.HasPrefix(token, "https://"), strings.HasPrefix(token, "http://"):
			return output.WithLinkFormat("%s", token)
		case strings.HasPrefix(token, "<"), strings.HasPrefix(token, "["):
			return output.WithWarningFormat("%s", token)
		default:
			return output.WithHighLightFormat("%s", token)
		}
	})
}
