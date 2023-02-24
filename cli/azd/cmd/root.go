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
)

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
			HelpOptions: actions.ActionHelpOptions{
				Description: getCmdRestoreHelpDescription,
				Footer:      getCmdRestoreHelpFooter,
			},
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
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdInitHelpDescription,
			Footer:      getCmdInitHelpFooter,
		},
	}).AddFlagCompletion("template", templateNameCompletion).
		UseMiddleware("ensureLogin", middleware.NewEnsureLoginMiddleware)

	root.
		Add("provision", &actions.ActionDescriptorOptions{
			Command:        newProvisionCmd(),
			FlagsResolver:  newProvisionFlags,
			ActionResolver: newProvisionAction,
			OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
			DefaultFormat:  output.NoneFormat,
			HelpOptions: actions.ActionHelpOptions{
				Description: getCmdProvisionHelpDescription,
				Footer:      getCmdHelpDefaultFooter,
			},
		}).
		UseMiddleware("hooks", middleware.NewHooksMiddleware)

	root.
		Add("deploy", &actions.ActionDescriptorOptions{
			Command:        newDeployCmd(),
			FlagsResolver:  newDeployFlags,
			ActionResolver: newDeployAction,
			OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
			DefaultFormat:  output.NoneFormat,
			HelpOptions: actions.ActionHelpOptions{
				Description: getCmdDeployHelpDescription,
				Footer:      getCmdDeployHelpFooter,
			},
		}).
		UseMiddleware("hooks", middleware.NewHooksMiddleware)

	root.
		Add("up", &actions.ActionDescriptorOptions{
			Command:        newUpCmd(),
			FlagsResolver:  newUpFlags,
			ActionResolver: newUpAction,
			OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
			DefaultFormat:  output.NoneFormat,
			HelpOptions: actions.ActionHelpOptions{
				Description: getCmdUpHelpDescription,
				Footer:      getCmdUpHelpFooter,
			},
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
			HelpOptions: actions.ActionHelpOptions{
				Description: getCmdDownHelpDescription,
				Footer:      getCmdDownHelpFooter,
			},
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

	// The help template has to be set after calling `BuildCommand()` to ensure the command tree is built
	cmd.SetHelpTemplate(generateCmdHelp(
		cmd,
		generateCmdHelpOptions{
			Description: getCmdHelpDefaultDescription,
			Commands:    func(c *cobra.Command) string { return getCmdHelpGroupedCommands(getCmdRootHelpCommands(c)) },
			Footer:      getCmdRootHelpFooter,
		}))

	return cmd
}

func getCmdRootHelpFooter(cmd *cobra.Command) string {
	return fmt.Sprintf("%s\n%s\n  %s %s %s %s\n  %s %s.\n    %s\n\n%s",
		getCommonFooterNote(cmd.CommandPath()),
		output.WithBold(output.WithUnderline(i18nGetText(i18nCmdRootHelpFooterQuickStart))),
		i18nGetText(i18nCmdRootHelpFooterQuickStartDetail),
		output.WithHighLightFormat(i18nGetText(i18nAzdUpTemplate)),
		output.WithWarningFormat("[%s]", i18nGetText(i18nTemplateName)),
		i18nGetText(i18nCmdRootHelpFooterQuickStartLast),
		output.WithGrayFormat(i18nGetText(i18nCmdRootHelpFooterQuickStartNote)),
		output.WithLinkFormat(i18nGetText(i18nAwesomeAzdUrl)),
		output.WithHighLightFormat(i18nGetText(i18nAzdUpNodeJsMongo)),
		generateHelpFindFillBug(),
	)
}

func getCmdRootHelpCommands(cmd *cobra.Command) (result string) {
	childrenCommands := cmd.Commands()
	groups := []i18nTextId{
		i18nCmdGroupTitleConfig, i18nCmdGroupTitleManage, i18nCmdGroupTitleMonitor, i18nCmdGroupTitleAbout}

	var commandGroups = make(map[i18nTextId][]string, len(groups))
	// Add hardcoded message for help, as there is not a command for it and we want it in the list
	commandGroups[i18nCmdGroupTitleAbout] = append(commandGroups[i18nCmdGroupTitleAbout],
		fmt.Sprintf("%s%s%s", "help", endOfTitleSentinel, i18nGetText(i18nCmdHelp)))

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

	var paragraph []string
	for _, title := range groups {
		paragraph = append(paragraph, fmt.Sprintf("  %s\n    %s\n",
			output.WithBold(i18nGetText(title)),
			strings.Join(commandGroups[title], "\n    ")))
	}
	return strings.Join(paragraph, "\n")
}
