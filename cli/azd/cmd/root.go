// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"

	// Importing for infrastructure provider plugin registrations

	"github.com/azure/azure-dev/cli/azd/pkg/ioc"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

// Creates the root Cobra command for AZD.
// staticHelp - False, except for running for doc generation
// middlewareChain - nil, except for running unit tests
func NewRootCmd(ctx context.Context, staticHelp bool, middlewareChain []*actions.MiddlewareRegistration) *cobra.Command {
	prevDir := ""
	opts := &internal.GlobalCommandOptions{GenerateStaticHelp: staticHelp}
	opts.EnableTelemetry = telemetry.IsTelemetryEnabled()

	productName := "The Azure Developer CLI"
	if opts.GenerateStaticHelp {
		productName = "The Azure Developer CLI (`azd`)"
	}

	rootCmd := &cobra.Command{
		Use:   "azd",
		Short: fmt.Sprintf("%s is an open-source tool that helps onboard and manage your application on Azure", productName),
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
	hooksActions(root)

	root.Add("version", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Print the version number of Azure Developer CLI.",
		},
		ActionResolver:   newVersionAction,
		FlagsResolver:    newVersionFlags,
		DisableTelemetry: true,
		OutputFormats:    []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:    output.NoneFormat,
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupAbout,
		},
	})

	root.Add("show", &actions.ActionDescriptorOptions{
		Command:        newShowCmd(),
		FlagsResolver:  newShowFlags,
		ActionResolver: newShowAction,
		OutputFormats:  []output.Format{output.JsonFormat},
		DefaultFormat:  output.NoneFormat,
	})

	//deprecate:cmd hide login
	login := newLoginCmd("")
	login.Hidden = true
	root.Add("login", &actions.ActionDescriptorOptions{
		Command:        login,
		FlagsResolver:  newLoginFlags,
		ActionResolver: newLoginAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	//deprecate:cmd hide logout
	logout := newLogoutCmd("")
	logout.Hidden = true
	root.Add("logout", &actions.ActionDescriptorOptions{
		Command:        logout,
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
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupConfig,
		},
	})

	root.
		Add("restore", &actions.ActionDescriptorOptions{
			Command:        newRestoreCmd(),
			FlagsResolver:  newRestoreFlags,
			ActionResolver: newRestoreAction,
			OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
			DefaultFormat:  output.NoneFormat,
			HelpOptions: actions.ActionHelpOptions{
				Description: getCmdRestoreHelpDescription,
				Footer:      getCmdRestoreHelpFooter,
			},
			GroupingOptions: actions.CommandGroupOptions{
				RootLevelHelp: actions.CmdGroupConfig,
			},
		}).
		UseMiddleware("hooks", middleware.NewHooksMiddleware)

	root.
		Add("build", &actions.ActionDescriptorOptions{
			Command:        newBuildCmd(),
			FlagsResolver:  newBuildFlags,
			ActionResolver: newBuildAction,
			OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
			DefaultFormat:  output.NoneFormat,
		}).
		UseMiddleware("hooks", middleware.NewHooksMiddleware)

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
			GroupingOptions: actions.CommandGroupOptions{
				RootLevelHelp: actions.CmdGroupManage,
			},
		}).
		UseMiddlewareWhen("hooks", middleware.NewHooksMiddleware, func(descriptor *actions.ActionDescriptor) bool {
			if onPreview, _ := descriptor.Options.Command.Flags().GetBool("preview"); onPreview {
				log.Println("Skipping provision hooks due to preview flag.")
				return false
			}
			return true
		})

	root.
		Add("package", &actions.ActionDescriptorOptions{
			Command:        newPackageCmd(),
			FlagsResolver:  newPackageFlags,
			ActionResolver: newPackageAction,
			OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
			DefaultFormat:  output.NoneFormat,
			HelpOptions: actions.ActionHelpOptions{
				Description: getCmdPackageHelpDescription,
				Footer:      getCmdPackageHelpFooter,
			},
			GroupingOptions: actions.CommandGroupOptions{
				RootLevelHelp: actions.CmdGroupManage,
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
			GroupingOptions: actions.CommandGroupOptions{
				RootLevelHelp: actions.CmdGroupManage,
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
			},
			GroupingOptions: actions.CommandGroupOptions{
				RootLevelHelp: actions.CmdGroupManage,
			},
		}).
		UseMiddleware("hooks", middleware.NewHooksMiddleware)

	root.Add("monitor", &actions.ActionDescriptorOptions{
		Command:        newMonitorCmd(),
		FlagsResolver:  newMonitorFlags,
		ActionResolver: newMonitorAction,
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdMonitorHelpDescription,
			Footer:      getCmdMonitorHelpFooter,
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupMonitor,
		},
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
			GroupingOptions: actions.CommandGroupOptions{
				RootLevelHelp: actions.CmdGroupManage,
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
		UseMiddleware("experimentation", middleware.NewExperimentationMiddleware).
		UseMiddlewareWhen("telemetry", middleware.NewTelemetryMiddleware, func(descriptor *actions.ActionDescriptor) bool {
			return !descriptor.Options.DisableTelemetry
		})

	ioc.RegisterInstance(ioc.Global, ctx)
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
	return fmt.Sprintf("%s\n%s\n%s\n\n%s\n\n%s",
		output.WithBold(output.WithUnderline("Deploying a sample application")),
		"Initialize from a sample application by running the "+
			output.WithHighLightFormat("azd init --template ")+
			output.WithWarningFormat("[%s]", "template name")+" command in an empty directory.",
		"Then, run "+output.WithHighLightFormat("azd up")+" to get the application up-and-running in Azure.",
		"To view a curated list of sample templates, run "+
			output.WithHighLightFormat("azd template list")+".\n"+
			"To view all available sample templates, including those submitted by the azd community, visit: "+
			output.WithLinkFormat("https://azure.github.io/awesome-azd")+".",
		getCmdHelpDefaultFooter(cmd),
	)
}

func getCmdRootHelpCommands(cmd *cobra.Command) (result string) {
	childrenCommands := cmd.Commands()
	groups := actions.GetGroupAnnotations()

	var commandGroups = make(map[string][]string, len(groups))
	// stores the longes line len
	max := 0

	for _, childCommand := range childrenCommands {
		// we rely on commands annotations for command grouping. Commands w/o annotation are ignored.
		if childCommand.Annotations == nil {
			continue
		}
		groupType, found := actions.GetGroupCommandAnnotation(childCommand)
		if !found {
			continue
		}
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
			output.WithBold(string(title)),
			strings.Join(commandGroups[string(title)], "\n    ")))
	}
	return strings.Join(paragraph, "\n")
}
