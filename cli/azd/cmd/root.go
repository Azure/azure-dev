// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"

	// Importing for infrastructure provider plugin registrations

	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/terraform"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
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

// Creates the root Cobra command for AZD.
// staticHelp - False, except for running for doc generation
// middlewareChain - nil, except for running unit tests
func NewRootCmd(staticHelp bool, middlewareChain []*actions.MiddlewareRegistration) *cobra.Command {
	prevDir := ""
	opts := &internal.GlobalCommandOptions{GenerateStaticHelp: staticHelp}
	opts.EnableTelemetry = telemetry.IsTelemetryEnabled()

	//productName := "The Azure Developer CLI"
	productName := i18nGetText(i18nProductName)
	if opts.GenerateStaticHelp {
		productName = i18nGetText(i18nDocsProductName)
	}

	rootCmd := &cobra.Command{
		Use:   "azd",
		Short: fmt.Sprintf(`%s %s`, productName, i18nGetText(i18nAzdShortHelp)),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if opts.Cwd != "" {
				current, err := os.Getwd()

				if err != nil {
					return err
				}

				prevDir = current

				if err := os.Chdir(opts.Cwd); err != nil {
					return fmt.Errorf("failed to change directory to %s: %w", opts.Cwd, err)
				}
			}

			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			// This is just for cleanliness and making writing tests simpler since
			// we can just remove the entire project folder afterwards.
			// In practical execution, this wouldn't affect much, since the CLI is exiting.
			if prevDir != "" {
				return os.Chdir(prevDir)
			}

			return nil
		},
		SilenceUsage:      true,
		DisableAutoGenTag: true,
	}

	rootCmd.CompletionOptions.HiddenDefaultCmd = true

	root := actions.NewActionDescriptor("azd", &actions.ActionDescriptorOptions{
		Command: rootCmd,
		FlagsResolver: func(cmd *cobra.Command) *internal.GlobalCommandOptions {
			rootCmd.PersistentFlags().StringVarP(&opts.Cwd, "cwd", "C", "", "Sets the current working directory.")
			rootCmd.PersistentFlags().
				BoolVar(&opts.EnableDebugLogging, "debug", false, "Enables debugging and diagnostics logging.")
			rootCmd.PersistentFlags().
				BoolVar(
					&opts.NoPrompt,
					"no-prompt",
					false,
					"Accepts the default value instead of prompting, or it fails if there is no default.")

			// The telemetry system is responsible for reading these flags value and using it to configure the telemetry
			// system, but we still need to add it to our flag set so that when we parse the command line with Cobra we
			// don't error due to an "unknown flag".
			var traceLogFile string
			var traceLogEndpoint string

			rootCmd.PersistentFlags().StringVar(&traceLogFile, "trace-log-file", "", "Write a diagnostics trace to a file.")
			_ = rootCmd.PersistentFlags().MarkHidden("trace-log-file")

			rootCmd.PersistentFlags().StringVar(
				&traceLogEndpoint, "trace-log-url", "", "Send traces to an Open Telemetry compatible endpoint.")
			_ = rootCmd.PersistentFlags().MarkHidden("trace-log-url")

			return opts
		},
	})

	configActions(root, opts)
	envActions(root)
	infraActions(root)
	pipelineActions(root)
	telemetryActions(root)
	templatesActions(root)
	authActions(root)

	versionCmd := &cobra.Command{
		Short: "Print the version number of Azure Developer CLI.",
	}
	annotateGroupCmd(versionCmd, cmdGroupAbout)
	root.Add("version", &actions.ActionDescriptorOptions{
		Command:          versionCmd,
		ActionResolver:   newVersionAction,
		FlagsResolver:    newVersionFlags,
		DisableTelemetry: true,
		OutputFormats:    []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:    output.NoneFormat,
	})

	root.Add("show", &actions.ActionDescriptorOptions{
		Command:        newShowCmd(),
		FlagsResolver:  newShowFlags,
		ActionResolver: newShowAction,
		OutputFormats:  []output.Format{output.JsonFormat},
		DefaultFormat:  output.NoneFormat,
	})

	root.
		Add("restore", &actions.ActionDescriptorOptions{
			Command:        restoreCmdDesign(),
			FlagsResolver:  newRestoreFlags,
			ActionResolver: newRestoreAction,
		}).
		UseMiddleware("hooks", middleware.NewHooksMiddleware)

	root.Add("login", &actions.ActionDescriptorOptions{
		Command:        newLoginCmd(),
		FlagsResolver:  newLoginFlags,
		ActionResolver: newLoginAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	root.Add("logout", &actions.ActionDescriptorOptions{
		Command:        newLogoutCmd(),
		ActionResolver: newLogoutAction,
	})

	root.Add("init", &actions.ActionDescriptorOptions{
		Command:        newInitCmd(),
		FlagsResolver:  newInitFlags,
		ActionResolver: newInitAction,
	}).AddFlagCompletion("template", templateNameCompletion).
		UseMiddleware("ensureLogin", middleware.NewEnsureLoginMiddleware)

	root.
		Add("provision", &actions.ActionDescriptorOptions{
			Command:        newProvisionCmd(),
			FlagsResolver:  newProvisionFlags,
			ActionResolver: newProvisionAction,
			OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
			DefaultFormat:  output.NoneFormat,
		}).
		UseMiddleware("hooks", middleware.NewHooksMiddleware)

	root.
		Add("deploy", &actions.ActionDescriptorOptions{
			Command:        newDeployCmd(),
			FlagsResolver:  newDeployFlags,
			ActionResolver: newDeployAction,
			OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
			DefaultFormat:  output.NoneFormat,
		}).
		UseMiddleware("hooks", middleware.NewHooksMiddleware)

	root.
		Add("up", &actions.ActionDescriptorOptions{
			Command:        newUpCmd(),
			FlagsResolver:  newUpFlags,
			ActionResolver: newUpAction,
			OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
			DefaultFormat:  output.NoneFormat,
		}).
		AddFlagCompletion("template", templateNameCompletion).
		UseMiddleware("hooks", middleware.NewHooksMiddleware)

	root.Add("monitor", &actions.ActionDescriptorOptions{
		Command:        newMonitorCmd(),
		FlagsResolver:  newMonitorFlags,
		ActionResolver: newMonitorAction,
	})

	root.
		Add("down", &actions.ActionDescriptorOptions{
			Command:        newDownCmd(),
			FlagsResolver:  newDownFlags,
			ActionResolver: newDownAction,
			OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
			DefaultFormat:  output.NoneFormat,
		}).
		UseMiddleware("hooks", middleware.NewHooksMiddleware)

	// Register any global middleware defined by the caller
	if len(middlewareChain) > 0 {
		for _, registration := range middlewareChain {
			root.UseMiddlewareWhen(registration.Name, registration.Resolver, registration.Predicate)
		}
	}

	// Global middleware registration
	root.
		UseMiddleware("debug", middleware.NewDebugMiddleware).
		UseMiddlewareWhen("telemetry", middleware.NewTelemetryMiddleware, func(descriptor *actions.ActionDescriptor) bool {
			return !descriptor.Options.DisableTelemetry
		})

	registerCommonDependencies(ioc.Global)
	cobraBuilder := NewCobraBuilder(ioc.Global)

	// Compose the hierarchy of action descriptions into cobra commands
	cmd, err := cobraBuilder.BuildCommand(root)

	if err != nil {
		// If their is a container registration issue or similar we'll get an error at this point
		// Error descriptions should be clear enough to resolve the issue
		panic(err)
	}

	// once the command is created, let's finalize the help template
	cmd.SetHelpTemplate(getRootCmdHelp(cmd))
	if opts.GenerateStaticHelp {
		cmd.Long = getRootCmdHelp(cmd)
	}

	return cmd
}

func getRootCmdHelp(cmd *cobra.Command) string {
	// root command doesn't use `cmd.Long`. It use Short for both.
	description := cmd.Short
	usage := fmt.Sprintf("%s\n  %s\n",
		output.WithBold(output.WithUnderline(i18nGetText(i18nUsage))), i18nGetText(i18nAzdUsage))
	commands := fmt.Sprintf("%s\n",
		output.WithBold(output.WithUnderline(i18nGetText(i18nCommands))))
	commandsDetails := getRootCommandsDetails(cmd)
	flags := fmt.Sprintf("%s\n",
		output.WithBold(output.WithUnderline(i18nGetText(i18nFlags))))
	flagsDetails := getFlagsDetails(cmd)
	footer := getRootCmdFooter(cmd)
	return fmt.Sprintf("\n%s\n\n%s\n%s%s%s%s\n%s\n",
		description,
		usage,
		commands,
		commandsDetails,
		flags,
		flagsDetails,
		footer)
}

func getRootCmdFooter(cmd *cobra.Command) string {
	return fmt.Sprintf("%s %s %s %s %s\n\n%s\n  %s %s %s %s\n  %s %s.\n    %s\n\n%s %s.\n",
		i18nGetText(i18nUse),
		output.WithHighLightFormat(i18nGetText(i18nAzd)),
		output.WithWarningFormat("[%s]", i18nGetText(i18nCommand)),
		output.WithHighLightFormat("--%s", i18nGetText(i18nHelp)),
		i18nGetText(i18nCmdRootHelpFooterTitle),
		output.WithBold(output.WithUnderline(i18nGetText(i18nCmdRootHelpFooterQuickStart))),
		i18nGetText(i18nCmdRootHelpFooterQuickStartDetail),
		output.WithHighLightFormat(i18nGetText(i18nAzdUpTemplate)),
		output.WithWarningFormat("[%s]", i18nGetText(i18nTemplateName)),
		i18nGetText(i18nCmdRootHelpFooterQuickStartLast),
		output.WithGrayFormat(i18nGetText(i18nCmdRootHelpFooterQuickStartNote)),
		output.WithLinkFormat(i18nGetText(i18nAwesomeAzdUrl)),
		output.WithHighLightFormat(i18nGetText(i18nAzdUpNodeJsMongo)),
		i18nGetText(i18nCmdRootHelpFooterReportBug),
		output.WithLinkFormat(i18nGetText(i18nAzdHats)),
	)
}

func getFlagsDetails(cmd *cobra.Command) (result string) {
	persistedFlags := cmd.NonInheritedFlags()
	var lines []string
	max := 0
	persistedFlags.VisitAll(func(flag *pflag.Flag) {
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

func getRootCommandsDetails(cmd *cobra.Command) (result string) {
	childrenCommands := cmd.Commands()
	groups := []i18nTextId{
		i18nCmdGroupTitleConfig, i18nCmdGroupTitleManage, i18nCmdGroupTitleMonitor, i18nCmdGroupTitleAbout}

	var commandGroups = make(map[i18nTextId][]string, len(groups))
	// Add hardcoded message for help, as there is not a command for it and we want it in the list
	commandGroups[i18nCmdGroupTitleAbout] = append(commandGroups[i18nCmdGroupTitleAbout],
		fmt.Sprintf("%s%s%s", i18nGetText(i18nHelp), endOfTitleSentinel, i18nGetText(i18nCmdHelp)))

	// stores the longes line len
	max := 0

	for _, childCommand := range childrenCommands {
		// we rely on commands annotations for command grouping. Commands w/o annotation are ignored.
		if childCommand.Annotations == nil {
			continue
		}
		group, found := childCommand.Annotations[cmdGrouper]
		if !found {
			continue
		}
		groupType := i18nTextId(group)

		commandName := childCommand.Name()
		commandNameLen := len(commandName)
		if commandNameLen > max {
			max = commandNameLen
		}
		commandGroups[groupType] = append(commandGroups[groupType],
			fmt.Sprintf("%s%s%s", commandName, endOfTitleSentinel, childCommand.Short))
	}
	// align all lines
	for id := range commandGroups {
		alignTitles(commandGroups[id], max)
	}

	for _, title := range groups {
		result += fmt.Sprintf("  %s\n    %s\n\n",
			output.WithBold(i18nGetText(title)),
			strings.Join(commandGroups[title], "\n    "))
	}
	return result
}
